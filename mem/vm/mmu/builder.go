package mmu

import (
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// A Builder can build MMU component
type Builder struct {
	engine                   sim.Engine
	freq                     sim.Freq
	log2PageSize             uint64
	pageTable                vm.PageTable
	migrationServiceProvider sim.Port
	pageFaultServiceProvider sim.Port
	maxNumReqInFlight        int
	pageWalkingLatency       int

	migrationPolicy vm.MigrationPolicy
	useOASIS        bool
}

// MakeBuilder creates a new builder
func MakeBuilder() Builder {
	return Builder{
		freq:              1 * sim.GHz,
		log2PageSize:      12,
		maxNumReqInFlight: 16,
	}
}

// WithEngine sets the engine to be used with the MMU
func (b Builder) WithEngine(engine sim.Engine) Builder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency that the MMU to work at
func (b Builder) WithFreq(freq sim.Freq) Builder {
	b.freq = freq
	return b
}

// WithLog2PageSize sets the page size that the mmu support.
func (b Builder) WithLog2PageSize(log2PageSize uint64) Builder {
	b.log2PageSize = log2PageSize
	return b
}

// WithPageTable sets the page table that the MMU uses.
func (b Builder) WithPageTable(pageTable vm.PageTable) Builder {
	b.pageTable = pageTable
	return b
}

// WithMigrationServiceProvider sets the destination port that can perform
// page migration.
func (b Builder) WithMigrationServiceProvider(p sim.Port) Builder {
	b.migrationServiceProvider = p
	return b
}

func (b Builder) WithPageFaultServiceProvider(p sim.Port) Builder {
	b.pageFaultServiceProvider = p
	return b
}

// WithMaxNumReqInFlight sets the number of requests can be concurrently
// processed by the MMU.
func (b Builder) WithMaxNumReqInFlight(n int) Builder {
	b.maxNumReqInFlight = n
	return b
}

// WithPageWalkingLatency sets the number of cycles required for walking a page
// table.
func (b Builder) WithPageWalkingLatency(n int) Builder {
	b.pageWalkingLatency = n
	return b
}

func (b Builder) WithPageMigrationPolicy(policy vm.MigrationPolicy) Builder {
	b.migrationPolicy = policy
	return b
}

func (b Builder) WithOASIS(oasis bool) Builder {
	b.useOASIS = oasis
	return b
}

// Build returns a newly created MMU component
func (b Builder) Build(name string) *MMU {
	mmu := new(MMU)
	mmu.TickingComponent = *sim.NewTickingComponent(
		name, b.engine, b.freq, mmu)

	b.createPorts(name, mmu)
	b.createPageTable(mmu)
	b.configureInternalStates(mmu)
	mmu.migrationPolicy = b.migrationPolicy
	mmu.useOASIS = b.useOASIS
	mmu.ROCopies = make(map[uint64][]uint64)
	mmu.PageFaultCount = make([]uint64, 4)
	mmu.TranslationReqCount = make([]uint64, 4)
	return mmu
}

func (b Builder) configureInternalStates(mmu *MMU) {
	mmu.MigrationServiceProvider = b.migrationServiceProvider
	mmu.PageFaultServiceProvider = b.pageFaultServiceProvider
	mmu.migrationQueueSize = 4096
	mmu.maxRequestsInFlight = b.maxNumReqInFlight
	mmu.latency = b.pageWalkingLatency
	mmu.PageAccessedByDeviceID = make(map[uint64][]uint64)
}

func (b Builder) createPageTable(mmu *MMU) {
	if b.pageTable != nil {
		mmu.pageTable = b.pageTable
	} else {
		mmu.pageTable = vm.NewPageTable(b.log2PageSize)
	}
}

func (b Builder) createPorts(name string, mmu *MMU) {
	mmu.topPort = sim.NewLimitNumMsgPort(mmu, 4096, name+".ToTop")
	mmu.AddPort("Top", mmu.topPort)
	mmu.migrationPort = sim.NewLimitNumMsgPort(mmu, 16, name+".MigrationPort")
	mmu.AddPort("Migration", mmu.migrationPort)
	mmu.pageFaultPort = sim.NewLimitNumMsgPort(mmu, 16, name+".PageFaultPort")
	mmu.AddPort("PageFault", mmu.pageFaultPort)
	mmu.toGMMUs = sim.NewLimitNumMsgPort(mmu, 16, name+".ToGMMUs")
	mmu.AddPort("ToGMMUs", mmu.toGMMUs)
	mmu.GMMUPorts = make(map[uint64]sim.Port)
	mmu.topSender = sim.NewBufferedSender(
		mmu.topPort, sim.NewBuffer(name+".TopSenderBuffer", 4096))
}
