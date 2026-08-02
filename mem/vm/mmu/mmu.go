package mmu

import (
	"log"
	"reflect"
	"slices"

	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

type transaction struct {
	req       *vm.TranslationReq
	page      vm.Page
	cycleLeft int
	migration *vm.PageMigrationReqToDriver
}

// MMU is the default mmu implementation. It is also an akita Component.
type MMU struct {
	sim.TickingComponent

	topPort       sim.Port
	migrationPort sim.Port
	pageFaultPort sim.Port

	MigrationServiceProvider sim.Port
	PageFaultServiceProvider sim.Port

	topSender sim.BufferedSender

	pageTable           vm.PageTable
	latency             int
	maxRequestsInFlight int

	walkingTranslations      []transaction
	migrationQueue           []transaction
	migrationQueueSize       int
	currentOnDemandMigration transaction
	isDoingMigration         bool

	toRemoveFromPTW        []int
	PageAccessedByDeviceID map[uint64][]uint64

	migrationPolicy   vm.MigrationPolicy
	useOASIS          bool
	pendingPageFaults []*vm.PageFaultNotification

	toGMMUs   sim.Port
	GMMUPorts map[uint64]sim.Port

	ROCopies           map[uint64][]uint64 // vaddr -> list of GPUs with copy of page
	invalidatePageReqs []*vm.InvalidatePageReq
	makePageROReqs     []*vm.MakePageReadOnlyReq
	updatePolicyReqs   []*vm.UpdatePolicyReq

	PageFaultCount      []uint64
	TranslationReqCount []uint64
}

// Tick defines how the MMU update state each cycle
func (mmu *MMU) Tick(now sim.VTimeInSec) bool {
	madeProgress := false

	madeProgress = mmu.topSender.Tick(now) || madeProgress
	madeProgress = mmu.sendMigrationToDriver(now) || madeProgress
	madeProgress = mmu.walkPageTable(now) || madeProgress
	madeProgress = mmu.processMigrationReturn(now) || madeProgress
	madeProgress = mmu.parseFromTop(now) || madeProgress
	madeProgress = mmu.sendPageFaultNotifications(now) || madeProgress
	madeProgress = mmu.receivePageFaultRsps(now) || madeProgress
	madeProgress = mmu.retryDupReqs(now) || madeProgress

	return madeProgress
}

func (mmu *MMU) walkPageTable(now sim.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < len(mmu.walkingTranslations); i++ {
		if mmu.walkingTranslations[i].cycleLeft > 0 {
			mmu.walkingTranslations[i].cycleLeft--
			madeProgress = true
			continue
		}

		madeProgress = mmu.finalizePageWalk(now, i) || madeProgress
	}

	tmp := mmu.walkingTranslations[:0]
	for i := 0; i < len(mmu.walkingTranslations); i++ {
		if !mmu.toRemove(i) {
			tmp = append(tmp, mmu.walkingTranslations[i])
		}
	}
	mmu.walkingTranslations = tmp
	mmu.toRemoveFromPTW = nil

	return madeProgress
}

func (mmu *MMU) finalizePageWalk(
	now sim.VTimeInSec,
	walkingIndex int,
) bool {
	req := mmu.walkingTranslations[walkingIndex].req
	page, found := mmu.pageTable.Find(req.PID, req.VAddr)

	if !found {
		panic("page not found")
	}

	mmu.walkingTranslations[walkingIndex].page = page

	if page.IsMigrating {
		return mmu.addTransactionToMigrationQueue(walkingIndex)
	}

	if mmu.pageNeedMigrate(mmu.walkingTranslations[walkingIndex]) {
		return mmu.addTransactionToMigrationQueue(walkingIndex)
	}

	return mmu.doPageWalkHit(now, walkingIndex)
}

func (mmu *MMU) notifyPageFault(page vm.Page, req *vm.TranslationReq) {
	mmu.PageFaultCount[req.DeviceID-1]++
	log.Printf("[page fault counts per device] %v", mmu.PageFaultCount)
	notif := vm.PageFaultNotificationBuilder{}.
		WithSendTime(0). // set at actual send time below
		WithSrc(mmu.pageFaultPort).
		WithDst(mmu.PageFaultServiceProvider).
		WithPID(req.PID).
		WithVAddr(page.VAddr).
		WithWrite(req.Write).
		Build()

	mmu.pendingPageFaults = append(mmu.pendingPageFaults, notif)
}

func (mmu *MMU) sendPageFaultNotifications(now sim.VTimeInSec) bool {
	if len(mmu.pendingPageFaults) == 0 {
		return false
	}
	notif := mmu.pendingPageFaults[0]
	notif.SendTime = now
	err := mmu.pageFaultPort.Send(notif)
	if err != nil {
		return false
	}
	mmu.pendingPageFaults = mmu.pendingPageFaults[1:]
	return true
}

func (mmu *MMU) receivePageFaultRsps(now sim.VTimeInSec) bool {
	rsp := mmu.pageFaultPort.Retrieve(now)
	if rsp == nil {
		return false
	}
	res := rsp.(*vm.PageFaultNotificationRsp)
	if res.Changed {
		for vaddr := res.BaseVAddr; vaddr < res.BaseVAddr+res.Size; {
			page, found := mmu.pageTable.Find(res.PID, vaddr)
			if found {
				if !(page.MigrationPolicy == vm.PolicyDuplication && len(mmu.ROCopies[vaddr]) > 1) {
					// update policy in mmu if previous policy isn't duplication or only one gpu has it
					page.MigrationPolicy = res.NewPolicy
					mmu.pageTable.Update(page)
					// notify gmmu of page's owner to update policy
					port := mmu.GMMUPorts[page.DeviceID-1]
					req := vm.UpdatePolicyReqBuilder{}.
						WithSendTime(now).
						WithSrc(mmu.toGMMUs).
						WithDst(port).
						WithPID(res.PID).
						WithVAddr(vaddr).
						WithNewPolicy(res.NewPolicy).
						Build()
					e := mmu.toGMMUs.Send(req)
					if e != nil {
						mmu.updatePolicyReqs = append(mmu.updatePolicyReqs, req)
					}
				} else {
					// invaliate page in gpus otherwirse
					page.MigrationPolicy = res.NewPolicy
					mmu.pageTable.Update(page)
					for _, gpu := range mmu.ROCopies[vaddr] {
						port := mmu.GMMUPorts[gpu-1]
						req := vm.NewInvalidatePageReq(now, mmu.toGMMUs, port)
						req.PID = res.PID
						req.VAddr = vaddr
						e := mmu.toGMMUs.Send(req)
						if e != nil {
							mmu.invalidatePageReqs = append(mmu.invalidatePageReqs, req)
						}
					}
					delete(mmu.ROCopies, vaddr)
				}
				vaddr += page.PageSize
			} else {
				vaddr += 4096
			}
		}
	}
	return true
}

func (mmu *MMU) addTransactionToMigrationQueue(walkingIndex int) bool {
	if len(mmu.migrationQueue) >= mmu.migrationQueueSize {
		return false
	}

	mmu.toRemoveFromPTW = append(mmu.toRemoveFromPTW, walkingIndex)
	mmu.migrationQueue = append(mmu.migrationQueue,
		mmu.walkingTranslations[walkingIndex])

	page := mmu.walkingTranslations[walkingIndex].page
	page.IsMigrating = true
	mmu.pageTable.Update(page)

	return true
}

func (mmu *MMU) pageNeedMigrate(walking transaction) bool {
	page := walking.page

	if page.MigrationPolicy != vm.PolicyDuplication && walking.req.DeviceID == page.DeviceID {
		return false
	}

	if !page.Unified {
		return false
	}

	if page.IsPinned {
		return false
	}

	// notify driver to record page fault in object table
	if mmu.useOASIS && mmu.pageFaultPort != nil {
		mmu.notifyPageFault(page, walking.req)
	}

	switch page.MigrationPolicy {
	case vm.PolicyOnTouch:
		return true

	case vm.PolicyAccessCounter:
		return walking.req.Migrate

	case vm.PolicyDuplication:
		return true

	default:
		return true // unset/zero value = on-touch, safe fallback
	}
}

func (mmu *MMU) doPageWalkHit(
	now sim.VTimeInSec,
	walkingIndex int,
) bool {
	if !mmu.topSender.CanSend(1) {
		return false
	}
	walking := mmu.walkingTranslations[walkingIndex]

	rsp := vm.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.topPort).
		WithDst(walking.req.Src).
		WithRspTo(walking.req.ID).
		WithPage(walking.page).
		Build()

	mmu.topSender.Send(rsp)
	mmu.toRemoveFromPTW = append(mmu.toRemoveFromPTW, walkingIndex)

	tracing.TraceReqComplete(walking.req, mmu)

	return true
}

// duplication reads and writes
//   - read:  hand out an additional copy. Nobody's existing copy becomes
//     stale, so no shootdown is needed, and page.DeviceID (the true
//     owner) does not change.
//   - write: every existing read-only copy becomes stale and must be
//     invalidated before the writer can proceed; ownership
//     then transfers to the writer, same as an ordinary migration.
func (mmu *MMU) sendDuplicationMigrationToDriver(
	now sim.VTimeInSec,
	trans transaction,
	page vm.Page,
) bool {
	if mmu.isDoingMigration {
		return false
	}

	req := trans.req

	migrationInfo := new(vm.PageMigrationInfo)
	migrationInfo.GPUReqToVAddrMap = make(map[uint64][]uint64)
	migrationInfo.GPUReqToVAddrMap[req.DeviceID] =
		append(migrationInfo.GPUReqToVAddrMap[req.DeviceID], req.VAddr)

	migrationReq := vm.NewPageMigrationReqToDriver(
		now, mmu.migrationPort, mmu.MigrationServiceProvider)
	migrationReq.PID = page.PID
	migrationReq.PageSize = page.PageSize
	migrationReq.CurrPageHostGPU = page.DeviceID
	migrationReq.MigrationInfo = migrationInfo
	migrationReq.RespondToTop = true
	migrationReq.IsDuplication = true
	migrationReq.Write = req.Write

	if len(mmu.ROCopies[page.VAddr]) == 0 {
		mmu.ROCopies[page.VAddr] = append(mmu.ROCopies[page.VAddr], page.DeviceID)
	}
	migrationReq.CurrAccessingGPUs = unique(append(mmu.ROCopies[page.VAddr], req.DeviceID))
	// log.Printf("handling duplication write[%v], vaddr[%v], curraccessinggpus[%v]\n", req.Write, page.VAddr, migrationReq.CurrAccessingGPUs)

	err := mmu.migrationPort.Send(migrationReq)
	if err != nil {
		return false
	}

	if req.Write {
		trans.page.IsMigrating = true
		trans.page.ReadOnly = false
		trans.page.DeviceID = req.DeviceID
		mmu.pageTable.Update(trans.page)
	} else if len(mmu.ROCopies[page.VAddr]) == 1 {
		// tell the single owner that the page is read only now
		trans.page.ReadOnly = true
		mmu.pageTable.Update(trans.page)
		owner := mmu.ROCopies[page.VAddr][0]
		req := vm.NewMakePageReadOnlyReq(now, mmu.toGMMUs, mmu.GMMUPorts[owner])
		req.PID = page.PID
		req.VAddr = page.VAddr
		e := mmu.toGMMUs.Send(req)
		if e != nil {
			mmu.makePageROReqs = append(mmu.makePageROReqs, req)
		}
	}

	trans.migration = migrationReq
	mmu.isDoingMigration = true
	mmu.currentOnDemandMigration = trans
	mmu.migrationQueue = mmu.migrationQueue[1:]

	if req.Write {
		for _, gpu := range mmu.ROCopies[page.VAddr] {
			if gpu == req.DeviceID {
				continue
			}
			log.Printf("write req, invaling gpu %v", gpu)
			req := vm.NewInvalidatePageReq(now, mmu.toGMMUs, mmu.GMMUPorts[gpu-1])
			req.PID = page.PID
			req.VAddr = page.VAddr
			e := mmu.toGMMUs.Send(req)
			if e != nil {
				mmu.invalidatePageReqs = append(mmu.invalidatePageReqs, req)
			}
		}
		log.Printf("done with write, req %v from %v", req.ID, req.DeviceID)
		mmu.ROCopies[page.VAddr] = []uint64{req.DeviceID}
	} else {
		mmu.ROCopies[page.VAddr] = unique(append(mmu.ROCopies[page.VAddr], req.DeviceID))
	}

	return true
}

func (mmu *MMU) sendMigrationToDriver(
	now sim.VTimeInSec,
) (madeProgress bool) {
	if len(mmu.migrationQueue) == 0 {
		return false
	}

	trans := mmu.migrationQueue[0]
	req := trans.req
	page, found := mmu.pageTable.Find(req.PID, req.VAddr)
	if !found {
		panic("page not found")
	}
	trans.page = page

	if (page.MigrationPolicy == vm.PolicyDuplication && !req.Write && slices.Contains(mmu.ROCopies[page.VAddr], req.DeviceID)) || // duplication read repeat send to mmu (shouldn't happen)
		(req.DeviceID == page.DeviceID && !(page.MigrationPolicy == vm.PolicyDuplication && req.Write)) || // page on the device, not duplication write
		page.IsPinned {
		mmu.sendTranlationRsp(now, trans)
		mmu.migrationQueue = mmu.migrationQueue[1:]
		mmu.markPageAsNotMigratingIfNotInTheMigrationQueue(page)

		return true
	}

	if mmu.isDoingMigration {
		return false
	}

	if page.MigrationPolicy == vm.PolicyDuplication {
		return mmu.sendDuplicationMigrationToDriver(now, trans, page)
	}

	migrationInfo := new(vm.PageMigrationInfo)
	migrationInfo.GPUReqToVAddrMap = make(map[uint64][]uint64)
	migrationInfo.GPUReqToVAddrMap[trans.req.DeviceID] =
		append(migrationInfo.GPUReqToVAddrMap[trans.req.DeviceID],
			trans.req.VAddr)

	mmu.PageAccessedByDeviceID[page.VAddr] =
		append(mmu.PageAccessedByDeviceID[page.VAddr], page.DeviceID)

	migrationReq := vm.NewPageMigrationReqToDriver(
		now, mmu.migrationPort, mmu.MigrationServiceProvider)
	migrationReq.PID = page.PID
	migrationReq.PageSize = page.PageSize
	migrationReq.CurrPageHostGPU = page.DeviceID
	migrationReq.MigrationInfo = migrationInfo
	migrationReq.CurrAccessingGPUs = unique(mmu.PageAccessedByDeviceID[page.VAddr])
	migrationReq.RespondToTop = true

	err := mmu.migrationPort.Send(migrationReq)
	if err != nil {
		return false
	}

	trans.page.IsMigrating = true
	mmu.pageTable.Update(trans.page)
	trans.migration = migrationReq
	mmu.isDoingMigration = true
	mmu.currentOnDemandMigration = trans
	mmu.migrationQueue = mmu.migrationQueue[1:]

	return true
}

func (mmu *MMU) markPageAsNotMigratingIfNotInTheMigrationQueue(
	page vm.Page,
) vm.Page {
	inQueue := false
	for _, t := range mmu.migrationQueue {
		if page.PAddr == t.page.PAddr {
			inQueue = true
			break
		}
	}

	if !inQueue {
		page.IsMigrating = false
		mmu.pageTable.Update(page)
		return page
	}

	return page
}

func (mmu *MMU) sendTranlationRsp(
	now sim.VTimeInSec,
	trans transaction,
) (madeProgress bool) {
	req := trans.req
	page := trans.page

	rsp := vm.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.topPort).
		WithDst(req.Src).
		WithRspTo(req.ID).
		WithPage(page).
		Build()
	mmu.topSender.Send(rsp)

	return true
}

func (mmu *MMU) processMigrationReturn(now sim.VTimeInSec) bool {
	item := mmu.migrationPort.Peek()
	if item == nil {
		return false
	}

	if !mmu.topSender.CanSend(1) {
		return false
	}

	rspFromDriver := item.(*vm.PageMigrationRspFromDriver)

	req := mmu.currentOnDemandMigration.req
	migReq := mmu.currentOnDemandMigration.migration

	var page vm.Page
	if migReq.IsDuplication && !migReq.Write {
		// Duplication read: translation we hand back to the requester
		// has to point at the copy's own local physical address.
		page = *rspFromDriver.CopyPage
	} else {
		var found bool
		page, found = mmu.pageTable.Find(req.PID, req.VAddr)
		if !found {
			panic("page not found")
		}
	}

	rsp := vm.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(mmu.topPort).
		WithDst(req.Src).
		WithRspTo(req.ID).
		WithPage(page).
		Build()
	mmu.topSender.Send(rsp)

	mmu.isDoingMigration = false

	if !(migReq.IsDuplication && !migReq.Write) {
		page = mmu.markPageAsNotMigratingIfNotInTheMigrationQueue(page)
		mmu.pageTable.Update(page)
	}

	mmu.migrationPort.Retrieve(now)

	return true
}

func (mmu *MMU) parseFromTop(now sim.VTimeInSec) bool {
	if len(mmu.walkingTranslations) >= mmu.maxRequestsInFlight {
		return false
	}

	req := mmu.topPort.Retrieve(now)
	if req == nil {
		return false
	}

	tracing.TraceReqReceive(req, mmu)

	switch req := req.(type) {
	case *vm.TranslationReq:
		mmu.TranslationReqCount[req.DeviceID-1]++
		log.Printf("[translation req counts per device] %v", mmu.TranslationReqCount)
		mmu.startWalking(req)
	default:
		log.Panicf("MMU canot handle request of type %s", reflect.TypeOf(req))
	}

	return true
}

func (mmu *MMU) startWalking(req *vm.TranslationReq) {
	translationInPipeline := transaction{
		req:       req,
		cycleLeft: mmu.latency,
	}

	mmu.walkingTranslations = append(mmu.walkingTranslations, translationInPipeline)
}

func (mmu *MMU) toRemove(index int) bool {
	for i := 0; i < len(mmu.toRemoveFromPTW); i++ {
		remove := mmu.toRemoveFromPTW[i]
		if remove == index {
			return true
		}
	}
	return false
}

func unique(intSlice []uint64) []uint64 {
	keys := make(map[int]bool)
	list := []uint64{}
	for _, entry := range intSlice {
		if _, value := keys[int(entry)]; !value {
			keys[int(entry)] = true
			list = append(list, entry)
		}
	}
	return list
}

func (mmu *MMU) retryDupReqs(now sim.VTimeInSec) bool {
	prog := false
	if len(mmu.invalidatePageReqs) != 0 {
		dst := mmu.invalidatePageReqs[:0]

		for _, req := range mmu.invalidatePageReqs {
			req.SendTime = now
			err := mmu.toGMMUs.Send(req)
			if err != nil {
				dst = append(dst, req)
			} else {
				prog = true
			}
		}
		mmu.invalidatePageReqs = dst
	}
	if len(mmu.makePageROReqs) != 0 {
		dst := mmu.makePageROReqs[:0]

		for _, req := range mmu.makePageROReqs {
			req.SendTime = now
			err := mmu.toGMMUs.Send(req)
			if err != nil {
				dst = append(dst, req)
			} else {
				prog = true
			}
		}
		mmu.makePageROReqs = dst
	}
	if len(mmu.updatePolicyReqs) != 0 {
		dst := mmu.updatePolicyReqs[:0]

		for _, req := range mmu.updatePolicyReqs {
			req.SendTime = now
			err := mmu.toGMMUs.Send(req)
			if err != nil {
				dst = append(dst, req)
			} else {
				prog = true
			}
		}
		mmu.updatePolicyReqs = dst
	}

	return prog
}
