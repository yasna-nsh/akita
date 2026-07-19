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
	remoteMemReqs       map[uint64]transaction

	toRemoveFromPTW        []int
	PageAccessedByDeviceID map[uint64][]uint64

	remainingAccesses map[uint64]int
}

// Tick defines how the gmmu update state each cycle
func (gmmu *Comp) Tick(now sim.VTimeInSec) bool {
	madeProgress := false

	madeProgress = gmmu.topSender.Tick(now) || madeProgress
	madeProgress = gmmu.parseFromTop(now) || madeProgress
	madeProgress = gmmu.walkPageTable(now) || madeProgress
	madeProgress = gmmu.fetchFromBottom(now) || madeProgress

	return madeProgress
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
		return !req.Write

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

	remaining--
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

	gmmu.remoteMemReqs[walking.VAddr] = gmmu.walkingTranslations[walkingIndex]

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
		}
		return gmmu.handleInvalidatePageReq(now, req)
	case *vm.UpdatePolicyReq:
		// TODO: cycle on pages in object and update their policy, update policy in tlbs too if requests flow through tlb to get to gmmu
		return true
	default:
		log.Panicf("gmmu canot handle request of type %s", reflect.TypeOf(req))
	}

	return true
}

func (gmmu *Comp) handleTranslationRsp(now sim.VTimeInSec, rsponse *vm.TranslationRsp) bool {
	reqTransaction := gmmu.remoteMemReqs[rsponse.Page.VAddr]
	if _, found := gmmu.pageTable.Find(rsponse.Page.PID, rsponse.Page.VAddr); found {
		gmmu.pageTable.Update(rsponse.Page)
	} else {
		gmmu.pageTable.Insert(rsponse.Page)
	}

	rsp := vm.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(gmmu.topPort).
		WithDst(reqTransaction.req.Src).
		WithRspTo(rsponse.ID).
		WithPage(rsponse.Page).
		Build()

	gmmu.topSender.Send(rsp)

	delete(gmmu.remoteMemReqs, rsponse.Page.VAddr)
	return true
}

func (gmmu *Comp) handleInvalidatePageReq(now sim.VTimeInSec, rsponse *vm.InvalidatePageReq) bool {
	// TODO: invalidate page in tlbs and l0
	return true
}
