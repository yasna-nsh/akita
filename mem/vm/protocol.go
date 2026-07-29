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
	PID       PID
	VAddr     uint64
	NewPolicy MigrationPolicy
}

// to invalidate read-only copies of pages when policy is duplication and one processor wants to write
type InvalidatePageReq struct {
	sim.MsgMeta
	VAddr uint64
	PID   PID
}

// from address translators to the GMMU
type UpdateCounterReq struct {
	sim.MsgMeta
	VAddr    uint64
	PID      PID
	DeviceID uint64
	Write    bool // specify if translation request is for a read or write
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

func (r *UpdateCounterReq) Meta() *sim.MsgMeta {
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

type UpdateCounterReqBuilder struct {
	sendTime sim.VTimeInSec
	src, dst sim.Port
	vAddr    uint64
	pid      PID
	deviceID uint64
	write    bool
}

func (b UpdateCounterReqBuilder) WithSendTime(
	t sim.VTimeInSec,
) UpdateCounterReqBuilder {
	b.sendTime = t
	return b
}

// WithSrc sets the source of the request to build.
func (b UpdateCounterReqBuilder) WithSrc(src sim.Port) UpdateCounterReqBuilder {
	b.src = src
	return b
}

// WithDst sets the destination of the request to build.
func (b UpdateCounterReqBuilder) WithDst(dst sim.Port) UpdateCounterReqBuilder {
	b.dst = dst
	return b
}

// WithVAddr sets the virtual address of the request to build.
func (b UpdateCounterReqBuilder) WithVAddr(vAddr uint64) UpdateCounterReqBuilder {
	b.vAddr = vAddr
	return b
}

func (b UpdateCounterReqBuilder) WithPID(pid PID) UpdateCounterReqBuilder {
	b.pid = pid
	return b
}

// WithDeviceID sets the GPU ID of the request to build.
func (b UpdateCounterReqBuilder) WithDeviceID(deviceID uint64) UpdateCounterReqBuilder {
	b.deviceID = deviceID
	return b
}

func (b UpdateCounterReqBuilder) WithWrite(w bool) UpdateCounterReqBuilder {
	b.write = w
	return b
}

func (b UpdateCounterReqBuilder) Build() *UpdateCounterReq {
	r := &UpdateCounterReq{}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src = b.src
	r.Dst = b.dst
	r.SendTime = b.sendTime
	r.VAddr = b.vAddr
	r.PID = b.pid
	r.DeviceID = b.deviceID
	r.Write = b.write
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

type PageFaultNotificationRsp struct {
	sim.MsgMeta
	PID       PID
	BaseVAddr uint64
	Size      uint64
	Changed   bool
	NewPolicy MigrationPolicy
}

func (m *PageFaultNotification) Meta() *sim.MsgMeta { return &m.MsgMeta }

func (m *PageFaultNotificationRsp) Meta() *sim.MsgMeta { return &m.MsgMeta }

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

type PageFaultNotificationRspBuilder struct {
	sendTime  sim.VTimeInSec
	src, dst  sim.Port
	pid       PID
	baseVAddr uint64
	size      uint64
	changed   bool
	newPolicy MigrationPolicy
}

func (b PageFaultNotificationRspBuilder) WithSendTime(t sim.VTimeInSec) PageFaultNotificationRspBuilder {
	b.sendTime = t
	return b
}
func (b PageFaultNotificationRspBuilder) WithSrc(p sim.Port) PageFaultNotificationRspBuilder {
	b.src = p
	return b
}
func (b PageFaultNotificationRspBuilder) WithDst(p sim.Port) PageFaultNotificationRspBuilder {
	b.dst = p
	return b
}
func (b PageFaultNotificationRspBuilder) WithPID(pid PID) PageFaultNotificationRspBuilder {
	b.pid = pid
	return b
}
func (b PageFaultNotificationRspBuilder) WithBaseVAddr(v uint64) PageFaultNotificationRspBuilder {
	b.baseVAddr = v
	return b
}
func (b PageFaultNotificationRspBuilder) WithSize(s uint64) PageFaultNotificationRspBuilder {
	b.size = s
	return b
}
func (b PageFaultNotificationRspBuilder) WithChanged(c bool) PageFaultNotificationRspBuilder {
	b.changed = c
	return b
}

func (b PageFaultNotificationRspBuilder) WithNewPolicy(p MigrationPolicy) PageFaultNotificationRspBuilder {
	b.newPolicy = p
	return b
}

func (b PageFaultNotificationRspBuilder) Build() *PageFaultNotificationRsp {
	m := &PageFaultNotificationRsp{PID: b.pid, BaseVAddr: b.baseVAddr, Size: b.size, Changed: b.changed, NewPolicy: b.newPolicy}
	m.ID = sim.GetIDGenerator().Generate()
	m.Src, m.Dst, m.SendTime = b.src, b.dst, b.sendTime
	return m
}

type UpdatePolicyReqBuilder struct {
	sendTime  sim.VTimeInSec
	src, dst  sim.Port
	pid       PID
	vAddr     uint64
	newPolicy MigrationPolicy
}

func (b UpdatePolicyReqBuilder) WithSendTime(t sim.VTimeInSec) UpdatePolicyReqBuilder {
	b.sendTime = t
	return b
}
func (b UpdatePolicyReqBuilder) WithSrc(p sim.Port) UpdatePolicyReqBuilder {
	b.src = p
	return b
}
func (b UpdatePolicyReqBuilder) WithDst(p sim.Port) UpdatePolicyReqBuilder {
	b.dst = p
	return b
}
func (b UpdatePolicyReqBuilder) WithPID(pid PID) UpdatePolicyReqBuilder {
	b.pid = pid
	return b
}
func (b UpdatePolicyReqBuilder) WithVAddr(v uint64) UpdatePolicyReqBuilder {
	b.vAddr = v
	return b
}

func (b UpdatePolicyReqBuilder) WithNewPolicy(p MigrationPolicy) UpdatePolicyReqBuilder {
	b.newPolicy = p
	return b
}

func (b UpdatePolicyReqBuilder) Build() *UpdatePolicyReq {
	m := &UpdatePolicyReq{PID: b.pid, VAddr: b.vAddr, NewPolicy: b.newPolicy}
	m.ID = sim.GetIDGenerator().Generate()
	m.Src, m.Dst, m.SendTime = b.src, b.dst, b.sendTime
	return m
}
