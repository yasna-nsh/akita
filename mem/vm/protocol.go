// Package vm provides the models for address translations
package vm

import (
	"github.com/sarchlab/akita/v3/sim"
)

// A TranslationReq asks the receiver component to translate the request.
type TranslationReq struct {
	sim.MsgMeta
	VAddr    uint64
	PID      PID
	DeviceID uint64
	Migrate  bool // modify this based on policy and access counter
	Write    bool // specify if translation request is for a read or write
}

// to update policy after OASIS's object controller decides on a new policy
type UpdatePolicyReq struct {
	sim.MsgMeta
	ObjID     uint8
	BaseVAddr uint64
	Size      uint64
	NewPolicy MigrationPolicy
}

// to invalidate read-only copies of pages when policy is duplication and one processor wants to write
type InvalidatePageReq struct {
	sim.MsgMeta
	VAddr uint64
	PID   PID
}

// Meta returns the meta data associated with the message.
func (r *TranslationReq) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

func (r *UpdatePolicyReq) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

func (r *InvalidatePageReq) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

// TranslationReqBuilder can build translation requests
type TranslationReqBuilder struct {
	sendTime sim.VTimeInSec
	src, dst sim.Port
	vAddr    uint64
	pid      PID
	deviceID uint64
	migrate  bool
	isWrite  bool
}

// WithSendTime sets the send time of the request to build.:w
func (b TranslationReqBuilder) WithSendTime(
	t sim.VTimeInSec,
) TranslationReqBuilder {
	b.sendTime = t
	return b
}

// WithSrc sets the source of the request to build.
func (b TranslationReqBuilder) WithSrc(src sim.Port) TranslationReqBuilder {
	b.src = src
	return b
}

// WithDst sets the destination of the request to build.
func (b TranslationReqBuilder) WithDst(dst sim.Port) TranslationReqBuilder {
	b.dst = dst
	return b
}

// WithVAddr sets the virtual address of the request to build.
func (b TranslationReqBuilder) WithVAddr(vAddr uint64) TranslationReqBuilder {
	b.vAddr = vAddr
	return b
}

// WithPID sets the virtual address of the request to build.
func (b TranslationReqBuilder) WithPID(pid PID) TranslationReqBuilder {
	b.pid = pid
	return b
}

// WithDeviceID sets the GPU ID of the request to build.
func (b TranslationReqBuilder) WithDeviceID(deviceID uint64) TranslationReqBuilder {
	b.deviceID = deviceID
	return b
}

func (b TranslationReqBuilder) WithMigrate(migrate bool) TranslationReqBuilder {
	b.migrate = migrate
	return b
}

func (b TranslationReqBuilder) WithWrite(w bool) TranslationReqBuilder {
	b.isWrite = w
	return b
}

// Build creates a new TranslationReq
func (b TranslationReqBuilder) Build() *TranslationReq {
	r := &TranslationReq{}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src = b.src
	r.Dst = b.dst
	r.SendTime = b.sendTime
	r.VAddr = b.vAddr
	r.PID = b.pid
	r.DeviceID = b.deviceID
	r.Migrate = b.migrate
	r.Write = b.isWrite
	return r
}

// A TranslationRsp is the respond for a TranslationReq. It carries the physical
// address.
type TranslationRsp struct {
	sim.MsgMeta
	RespondTo string // The ID of the request it replies
	Page      Page
}

// Meta returns the meta data associated with the message.
func (r *TranslationRsp) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

// GetRspTo returns the request ID that the respond is responding to.
func (r *TranslationRsp) GetRspTo() string {
	return r.RespondTo
}

// TranslationRspBuilder can build translation requests
type TranslationRspBuilder struct {
	sendTime sim.VTimeInSec
	src, dst sim.Port
	rspTo    string
	page     Page
}

// WithSendTime sets the send time of the message to build.
func (b TranslationRspBuilder) WithSendTime(
	t sim.VTimeInSec,
) TranslationRspBuilder {
	b.sendTime = t
	return b
}

// WithSrc sets the source of the respond to build.
func (b TranslationRspBuilder) WithSrc(src sim.Port) TranslationRspBuilder {
	b.src = src
	return b
}

// WithDst sets the destination of the respond to build.
func (b TranslationRspBuilder) WithDst(dst sim.Port) TranslationRspBuilder {
	b.dst = dst
	return b
}

// WithRspTo sets the request ID of the respond to build.
func (b TranslationRspBuilder) WithRspTo(rspTo string) TranslationRspBuilder {
	b.rspTo = rspTo
	return b
}

// WithPage sets the page of the respond to build.
func (b TranslationRspBuilder) WithPage(page Page) TranslationRspBuilder {
	b.page = page
	return b
}

// Build creates a new TranslationRsp
func (b TranslationRspBuilder) Build() *TranslationRsp {
	r := &TranslationRsp{}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src = b.src
	r.Dst = b.dst
	r.SendTime = b.sendTime
	r.RespondTo = b.rspTo
	r.Page = b.page
	return r
}

type PageMigrationInfo struct {
	GPUReqToVAddrMap map[uint64][]uint64
}

// PageMigrationReqToDriver is a req to driver from MMU to start page migration process
type PageMigrationReqToDriver struct {
	sim.MsgMeta

	StartTime         sim.VTimeInSec
	EndTime           sim.VTimeInSec
	MigrationInfo     *PageMigrationInfo
	CurrAccessingGPUs []uint64
	PID               PID
	CurrPageHostGPU   uint64
	PageSize          uint64
	RespondToTop      bool
}

// Meta returns the meta data associated with the message.
func (m *PageMigrationReqToDriver) Meta() *sim.MsgMeta {
	return &m.MsgMeta
}

// NewPageMigrationReqToDriver creates a PageMigrationReqToDriver.
func NewPageMigrationReqToDriver(
	time sim.VTimeInSec,
	src, dst sim.Port,
) *PageMigrationReqToDriver {
	cmd := new(PageMigrationReqToDriver)
	cmd.SendTime = time
	cmd.Src = src
	cmd.Dst = dst
	return cmd
}

// PageMigrationRspFromDriver is a rsp from driver to MMU marking completion of migration
type PageMigrationRspFromDriver struct {
	sim.MsgMeta

	StartTime sim.VTimeInSec
	EndTime   sim.VTimeInSec
	VAddr     []uint64
	RspToTop  bool
}

// Meta returns the meta data associated with the message.
func (m *PageMigrationRspFromDriver) Meta() *sim.MsgMeta {
	return &m.MsgMeta
}

// NewPageMigrationRspFromDriver creates a new PageMigrationRspFromDriver.
func NewPageMigrationRspFromDriver(
	time sim.VTimeInSec,
	src, dst sim.Port,
) *PageMigrationRspFromDriver {
	cmd := new(PageMigrationRspFromDriver)
	cmd.SendTime = time
	cmd.Src = src
	cmd.Dst = dst
	return cmd
}

// mmu notifies driver of page fault to update otable
type PageFaultNotification struct {
	sim.MsgMeta
	PID   PID
	VAddr uint64
	Write bool
}

func (m *PageFaultNotification) Meta() *sim.MsgMeta { return &m.MsgMeta }

type PageFaultNotificationBuilder struct {
	sendTime sim.VTimeInSec
	src, dst sim.Port
	pid      PID
	vAddr    uint64
	write    bool
}

func (b PageFaultNotificationBuilder) WithSendTime(t sim.VTimeInSec) PageFaultNotificationBuilder {
	b.sendTime = t
	return b
}
func (b PageFaultNotificationBuilder) WithSrc(p sim.Port) PageFaultNotificationBuilder {
	b.src = p
	return b
}
func (b PageFaultNotificationBuilder) WithDst(p sim.Port) PageFaultNotificationBuilder {
	b.dst = p
	return b
}
func (b PageFaultNotificationBuilder) WithPID(pid PID) PageFaultNotificationBuilder {
	b.pid = pid
	return b
}
func (b PageFaultNotificationBuilder) WithVAddr(v uint64) PageFaultNotificationBuilder {
	b.vAddr = v
	return b
}
func (b PageFaultNotificationBuilder) WithWrite(w bool) PageFaultNotificationBuilder {
	b.write = w
	return b
}

func (b PageFaultNotificationBuilder) Build() *PageFaultNotification {
	m := &PageFaultNotification{PID: b.pid, VAddr: b.vAddr, Write: b.write}
	m.ID = sim.GetIDGenerator().Generate()
	m.Src, m.Dst, m.SendTime = b.src, b.dst, b.sendTime
	return m
}
