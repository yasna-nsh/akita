// Package gmmu provides the implementation of the Graphics Memory Management Unit (GMMU).
// It includes structures and methods for handling memory translation, page migration,
// and other related operations within the virtual memory system.
package gmmu

import (
	"log"
	"reflect"

	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

type transaction struct {
	req       *vm.TranslationReq
	page      vm.Page
	cycleLeft int
}

// Comp is the default gmmu implementation. It is also an akita Component.
type Comp struct {
	sim.TickingComponent

	deviceID uint64

	topPort    sim.Port
	bottomPort sim.Port
	LowModule  sim.Port

	topSender    sim.BufferedSender
	bottomSender sim.BufferedSender

	pageTable           vm.PageTable
	latency             int
	maxRequestsInFlight int

	walkingTranslations []transaction
	remoteMemReqs       map[string]transaction

	toRemoveFromPTW        []int
	PageAccessedByDeviceID map[uint64][]uint64

	ToAT                    sim.Port
	remainingAccesses       map[uint64]int
	pendingForcedMigrations map[uint64]vm.PID

	l2TLBTopDst                sim.Port
	pendingInvalidateReqs      []*vm.InvalidatePageReq
	pendingMakeReadOnlyReqs    []*vm.MakePageReadOnlyReq
	pendingUpdatePolicies      []*vm.UpdatePolicyReq
	pendingForcedMigrationReqs []*vm.TranslationReq
	pendingUpdateDIDReqs       []*vm.UpdateDIDReq
}

// Tick defines how the gmmu update state each cycle
func (gmmu *Comp) Tick(now sim.VTimeInSec) bool {
	madeProgress := false

	madeProgress = gmmu.topSender.Tick(now) || madeProgress
	madeProgress = gmmu.parseFromTop(now) || madeProgress
	madeProgress = gmmu.walkPageTable(now) || madeProgress
	madeProgress = gmmu.fetchFromBottom(now) || madeProgress
	madeProgress = gmmu.fetchFromAT(now) || madeProgress
	madeProgress = gmmu.handleInvalidatePageReq(now) || madeProgress
	madeProgress = gmmu.handleMakePageReadOnlyReq(now) || madeProgress
	madeProgress = gmmu.handleUpdatePolicyReq(now) || madeProgress
	madeProgress = gmmu.handleForcedMigrations(now) || madeProgress
	madeProgress = gmmu.handleUpdateDIDReqs(now) || madeProgress

	return madeProgress
}

func (gmmu *Comp) SetL2TLBTopDst(p sim.Port) {
	gmmu.l2TLBTopDst = p
}

func (gmmu *Comp) parseFromTop(now sim.VTimeInSec) bool {
	if len(gmmu.walkingTranslations) >= gmmu.maxRequestsInFlight {
		return false
	}

	req := gmmu.topPort.Retrieve(now)
	if req == nil {
		return false
	}

	tracing.TraceReqReceive(req, gmmu)

	switch req := req.(type) {
	case *vm.TranslationReq:
		gmmu.startWalking(req)

		// fmt.Printf("%0.9f,%s,GMMUParseFromTop,%s\n",
		// 	float64(now), gmmu.topPort.Name(), req.TaskID)

	default:
		log.Panicf("gmmu canot handle request of type %s", reflect.TypeOf(req))
	}

	return true
}

func (gmmu *Comp) startWalking(req *vm.TranslationReq) {
	translationInPipeline := transaction{
		req:       req,
		cycleLeft: gmmu.latency,
	}

	gmmu.walkingTranslations = append(gmmu.walkingTranslations, translationInPipeline)
}

func (gmmu *Comp) walkPageTable(now sim.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < len(gmmu.walkingTranslations); i++ {
		if gmmu.walkingTranslations[i].cycleLeft > 0 {
			gmmu.walkingTranslations[i].cycleLeft--
			madeProgress = true
			continue
		}
		req := gmmu.walkingTranslations[i].req

		page, found := gmmu.pageTable.Find(req.PID, req.VAddr)

		if found && page.Valid && gmmu.canServeLocally(page, req) {
			madeProgress = gmmu.finalizePageWalk(now, i) || madeProgress
		} else {
			madeProgress = gmmu.processRemoteMemReq(now, i) || madeProgress
		}
	}

	tmp := gmmu.walkingTranslations[:0]
	for i := 0; i < len(gmmu.walkingTranslations); i++ {
		if !gmmu.toRemove(i) {
			tmp = append(tmp, gmmu.walkingTranslations[i])
		}
	}
	gmmu.walkingTranslations = tmp
	gmmu.toRemoveFromPTW = nil

	return madeProgress
}

// canServeLocally decides whether we should go back to the MMU or not
func (gmmu *Comp) canServeLocally(page vm.Page, req *vm.TranslationReq) bool {
	if page.MigrationPolicy == vm.PolicyDuplication && req.Write && page.ReadOnly {
		return false
	}
	if page.DeviceID == gmmu.deviceID {
		return true
	}
	if page.IsPinned {
		// pinned pages never migrate again, so a cached translation for
		// them stays valid regardless of policy
		return true
	}

	switch page.MigrationPolicy {
	case vm.PolicyAccessCounter:
		return !gmmu.accessCounterExpired(page.VAddr)

	case vm.PolicyDuplication:
		// if read, answer localy from read-only copy
		return !req.Write || !page.ReadOnly

	default: // on touch or other policy: always send to MMU
		return false
	}
}

// initialize/decrement the local access counter for vAddr, return true if hit zero
func (gmmu *Comp) accessCounterExpired(vAddr uint64) bool {
	if gmmu.remainingAccesses == nil {
		gmmu.remainingAccesses = make(map[uint64]int)
	}

	remaining, tracked := gmmu.remainingAccesses[vAddr]
	if !tracked {
		gmmu.remainingAccesses[vAddr] = vm.MigrationThreshold - 1
		return false
	}

	if remaining < 0 {
		delete(gmmu.remainingAccesses, vAddr)
		return true
	}
	gmmu.remainingAccesses[vAddr] = remaining
	return false
}

// request goes to mmu
func (gmmu *Comp) processRemoteMemReq(now sim.VTimeInSec, walkingIndex int) bool {
	// if !gmmu.bottomSender.CanSend(1) {
	// 	return false
	// }

	walking := gmmu.walkingTranslations[walkingIndex].req

	forceMigrate := false
	if page, found := gmmu.pageTable.Find(walking.PID, walking.VAddr); found && page.Valid {
		// if the page is found in page table and is valid but canServeLocally returns false, we end up here
		forceMigrate = true
	}

	req := vm.TranslationReqBuilder{}.
		WithSendTime(now).
		WithSrc(gmmu.bottomPort).
		WithDst(gmmu.LowModule).
		WithPID(walking.PID).
		WithVAddr(walking.VAddr).
		WithDeviceID(walking.DeviceID).
		WithMigrate(forceMigrate).
		WithWrite(walking.Write).
		Build()

	gmmu.remoteMemReqs[req.ID] = gmmu.walkingTranslations[walkingIndex]
	err := gmmu.bottomPort.Send(req)
	if err != nil {
		return false
	}

	gmmu.toRemoveFromPTW = append(gmmu.toRemoveFromPTW, walkingIndex)

	return true
}

func (gmmu *Comp) finalizePageWalk(
	now sim.VTimeInSec,
	walkingIndex int,
) bool {
	req := gmmu.walkingTranslations[walkingIndex].req
	page, _ := gmmu.pageTable.Find(req.PID, req.VAddr)
	gmmu.walkingTranslations[walkingIndex].page = page

	return gmmu.doPageWalkHit(now, walkingIndex)
}

func (gmmu *Comp) doPageWalkHit(
	now sim.VTimeInSec,
	walkingIndex int,
) bool {
	if !gmmu.topSender.CanSend(1) {
		return false
	}
	walking := gmmu.walkingTranslations[walkingIndex]

	rsp := vm.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(gmmu.topPort).
		WithDst(walking.req.Src).
		WithRspTo(walking.req.ID).
		WithPage(walking.page).
		Build()

	gmmu.topSender.Send(rsp)

	gmmu.toRemoveFromPTW = append(gmmu.toRemoveFromPTW, walkingIndex)

	tracing.TraceReqComplete(walking.req, gmmu)

	return true
}

func (gmmu *Comp) toRemove(index int) bool {
	for i := 0; i < len(gmmu.toRemoveFromPTW); i++ {
		remove := gmmu.toRemoveFromPTW[i]
		if remove == index {
			return true
		}
	}
	return false
}

func (gmmu *Comp) fetchFromBottom(now sim.VTimeInSec) bool {
	if !gmmu.topSender.CanSend(1) {
		return false
	}

	req := gmmu.bottomPort.Retrieve(now)
	if req == nil {
		return false
	}

	tracing.TraceReqReceive(req, gmmu)

	switch req := req.(type) {
	case *vm.TranslationRsp:
		return gmmu.handleTranslationRsp(now, req)

	case *vm.InvalidatePageReq:
		page, found := gmmu.pageTable.Find(req.PID, req.VAddr)
		if found {
			page.Valid = false
			gmmu.pageTable.Update(page)
			// log.Printf("Invalidating page %v in gpu %v after policy change.", req.VAddr, gmmu.Name())
		} else {
			log.Printf("Inconsistency betweem mmu and gmmu's pt, page %v isn't in gpu %v", req.VAddr, gmmu.Name())
		}
		gmmu.pendingInvalidateReqs = append(gmmu.pendingInvalidateReqs, req)

	case *vm.UpdatePolicyReq:
		gmmu.UpdatePagePolicy(req)
		gmmu.pendingUpdatePolicies = append(gmmu.pendingUpdatePolicies, req)

	case *vm.MakePageReadOnlyReq:
		page, found := gmmu.pageTable.Find(req.PID, req.VAddr)
		if found {
			page.ReadOnly = true
			gmmu.pageTable.Update(page)
		}
		gmmu.pendingMakeReadOnlyReqs = append(gmmu.pendingMakeReadOnlyReqs, req)

	default:
		log.Panicf("gmmu canot handle request of type %s", reflect.TypeOf(req))
	}

	return true
}

func (gmmu *Comp) UpdatePagePolicy(req *vm.UpdatePolicyReq) {
	page, find := gmmu.pageTable.Find(req.PID, req.VAddr)
	if find {
		log.Printf("page %v's policy changed from %v to %v in gpu %v", page.VAddr, page.MigrationPolicy, req.NewPolicy, gmmu.Name())
		page.MigrationPolicy = req.NewPolicy
		gmmu.pageTable.Update(page)
	}
}

func (gmmu *Comp) fetchFromAT(now sim.VTimeInSec) bool {
	req := gmmu.ToAT.Retrieve(now)
	if req == nil {
		return false
	}

	switch req := req.(type) {
	case *vm.UpdateCounterReq:
		return gmmu.updateCounter(now, req)
	default:
		log.Panicf("gmmu cannot handle request of type %s", reflect.TypeOf(req))
	}

	return true
}

// TODO: update L1TLBs and L2TLB and caches if duplicate invalidation happens
func (gmmu *Comp) handleTranslationRsp(now sim.VTimeInSec, rsponse *vm.TranslationRsp) bool {
	if _, ok := gmmu.pendingForcedMigrations[rsponse.Page.VAddr]; ok {
		delete(gmmu.pendingForcedMigrations, rsponse.Page.VAddr)
		gmmu.cachePage(rsponse.Page)
		req := vm.NewUpdateDIDReq(now, gmmu.topPort, gmmu.l2TLBTopDst)
		req.PID = rsponse.Page.PID
		req.VAddr = rsponse.Page.VAddr
		req.DeviceID = gmmu.deviceID
		gmmu.pendingUpdateDIDReqs = append(gmmu.pendingUpdateDIDReqs, req)
		return true
	}

	reqTransaction := gmmu.remoteMemReqs[rsponse.RespondTo]
	if rsponse.Page.MigrationPolicy == vm.PolicyDuplication {
		rsponse.Page.DeviceID = gmmu.deviceID
	}
	if _, found := gmmu.pageTable.Find(rsponse.Page.PID, rsponse.Page.VAddr); found {
		gmmu.pageTable.Update(rsponse.Page)
	} else {
		gmmu.pageTable.Insert(rsponse.Page)
		if rsponse.Page.MigrationPolicy == vm.PolicyAccessCounter {
			gmmu.initCounter(rsponse.Page.VAddr)
		}
	}

	rsp := vm.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(gmmu.topPort).
		WithDst(reqTransaction.req.Src).
		WithRspTo(reqTransaction.req.ID).
		WithPage(rsponse.Page).
		Build()

	gmmu.topSender.Send(rsp)

	delete(gmmu.remoteMemReqs, rsponse.RespondTo)
	return true
}

func (gmmu *Comp) initCounter(vAddr uint64) {
	if gmmu.remainingAccesses == nil {
		gmmu.remainingAccesses = make(map[uint64]int)
	}
	gmmu.remainingAccesses[vAddr] = vm.MigrationThreshold
}

func (gmmu *Comp) cachePage(page vm.Page) {
	if _, found := gmmu.pageTable.Find(page.PID, page.VAddr); found {
		gmmu.pageTable.Update(page)
	} else {
		gmmu.pageTable.Insert(page)
	}
	delete(gmmu.remainingAccesses, page.VAddr)
}

func (gmmu *Comp) handleUpdatePolicyReq(now sim.VTimeInSec) bool {
	if len(gmmu.pendingUpdatePolicies) == 0 {
		return false
	}
	rsponse := gmmu.pendingUpdatePolicies[0]
	req := vm.UpdatePolicyReqBuilder{}.
		WithSendTime(now).
		WithSrc(gmmu.topPort).
		WithDst(gmmu.l2TLBTopDst).
		WithPID(rsponse.PID).
		WithVAddr(rsponse.VAddr).
		WithNewPolicy(rsponse.NewPolicy).
		Build()
	e := gmmu.topPort.Send(req)
	if e != nil {
		return false
	}
	gmmu.pendingUpdatePolicies = gmmu.pendingUpdatePolicies[1:]
	return true
}

func (gmmu *Comp) handleInvalidatePageReq(now sim.VTimeInSec) bool {
	if len(gmmu.pendingInvalidateReqs) == 0 {
		return false
	}
	rsponse := gmmu.pendingInvalidateReqs[0]
	req := vm.NewInvalidatePageReq(now, gmmu.topPort, gmmu.l2TLBTopDst)
	req.PID = rsponse.PID
	req.VAddr = rsponse.VAddr
	e := gmmu.topPort.Send(req)
	if e != nil {
		return false
	}
	gmmu.pendingInvalidateReqs = gmmu.pendingInvalidateReqs[1:]
	return true
}

func (gmmu *Comp) handleMakePageReadOnlyReq(now sim.VTimeInSec) bool {
	if len(gmmu.pendingMakeReadOnlyReqs) == 0 {
		return false
	}
	rsponse := gmmu.pendingMakeReadOnlyReqs[0]
	req := vm.NewMakePageReadOnlyReq(now, gmmu.topPort, gmmu.l2TLBTopDst)
	req.PID = rsponse.PID
	req.VAddr = rsponse.VAddr
	e := gmmu.topPort.Send(req)
	if e != nil {
		return false
	}
	gmmu.pendingMakeReadOnlyReqs = gmmu.pendingMakeReadOnlyReqs[1:]
	return true
}

// decrease counter only if page is being tracked already
func (gmmu *Comp) updateCounter(now sim.VTimeInSec, req *vm.UpdateCounterReq) bool {
	page, found := gmmu.pageTable.Find(req.PID, req.VAddr)
	if !found || page.DeviceID == gmmu.deviceID {
		return true
	}

	if gmmu.remainingAccesses == nil {
		gmmu.remainingAccesses = make(map[uint64]int)
	}
	vAddr := req.VAddr
	remaining, tracked := gmmu.remainingAccesses[vAddr]
	if !tracked {
		return true
	}

	remaining--
	if remaining <= 0 {
		delete(gmmu.remainingAccesses, vAddr)
		log.Printf("[access-counter] req.vaddr=%v, forcing migration from %v to %v \n", req.VAddr, page.DeviceID, req.DeviceID)
		// TODO: request remote
		gmmu.requestForcedMigration(now, req.PID, req.VAddr, req.Write)

		return true
	}
	gmmu.remainingAccesses[vAddr] = remaining
	return true
}

func (gmmu *Comp) requestForcedMigration(now sim.VTimeInSec, pid vm.PID, vAddr uint64, write bool) {
	req := vm.TranslationReqBuilder{}.
		WithSendTime(now).
		WithSrc(gmmu.bottomPort).
		WithDst(gmmu.LowModule).
		WithPID(pid).
		WithVAddr(vAddr).
		WithDeviceID(gmmu.deviceID).
		WithMigrate(true).
		WithWrite(write).
		Build()
	err := gmmu.bottomPort.Send(req)
	if err != nil {
		gmmu.pendingForcedMigrationReqs = append(gmmu.pendingForcedMigrationReqs, req)
	}

	if gmmu.pendingForcedMigrations == nil {
		gmmu.pendingForcedMigrations = make(map[uint64]vm.PID)
	}
	gmmu.pendingForcedMigrations[vAddr] = pid
}

func (gmmu *Comp) handleForcedMigrations(now sim.VTimeInSec) bool {
	if len(gmmu.pendingForcedMigrationReqs) == 0 {
		return false
	}

	req := gmmu.pendingForcedMigrationReqs[0]
	req.SendTime = now
	err := gmmu.bottomPort.Send(req)
	if err != nil {
		return false
	}

	gmmu.pendingForcedMigrationReqs = gmmu.pendingForcedMigrationReqs[1:]
	return true
}

func (gmmu *Comp) handleUpdateDIDReqs(now sim.VTimeInSec) bool {
	if len(gmmu.pendingUpdateDIDReqs) == 0 {
		return false
	}
	req := gmmu.pendingUpdateDIDReqs[0]
	req.SendTime = now
	e := gmmu.topPort.Send(req)
	if e != nil {
		return false
	}
	gmmu.pendingUpdateDIDReqs = gmmu.pendingUpdateDIDReqs[1:]
	return true
}
