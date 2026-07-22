package remoteexec

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"sync/atomic"

	"github.com/Sheyiyuan/half-pi/modules/gateway-core/protocol"
)

// ProtectionRecord 是 Compact 范围选择需要保留的远程工作身份。
type ProtectionRecord struct {
	Kind            string
	ID              string
	RequestID       string
	LegacyCreatedAt int64
	StateUnknown    bool
}

// ProtectionSnapshot 是单个 conversation 的原子受保护工作视图。
type ProtectionSnapshot struct {
	Revision uint64
	Records  []ProtectionRecord
	Digest   string
}

// ProtectionSeed 是从持久化 Store 恢复的一条 conversation-scoped 保护记录。
type ProtectionSeed struct {
	SessionID string
	Record    ProtectionRecord
}

type protectionSession struct {
	revision uint64
	records  map[string]ProtectionRecord
}

type protectionState struct {
	sessions map[string]protectionSession
}

// ProtectionIndex 汇总 RemoteRun 与 RemoteTask 的非终态、stale 和未知记录。
// 写入通过不可变 state CAS 完成，读取不取得 Registry 或 TaskService 的锁。
type ProtectionIndex struct {
	state atomic.Pointer[protectionState]
}

// NewProtectionIndex 创建空的远程工作保护索引。
func NewProtectionIndex() *ProtectionIndex {
	index := &ProtectionIndex{}
	index.state.Store(&protectionState{sessions: make(map[string]protectionSession)})
	return index
}

// Snapshot 返回指定 conversation 的稳定排序记录、单调 revision 和规范摘要。
func (p *ProtectionIndex) Snapshot(sessionID string) ProtectionSnapshot {
	if p == nil {
		return emptyProtectionSnapshot()
	}
	state := p.state.Load()
	session := state.sessions[sessionID]
	records := make([]ProtectionRecord, 0, len(session.records))
	for _, record := range session.records {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Kind != records[j].Kind {
			return records[i].Kind < records[j].Kind
		}
		return records[i].ID < records[j].ID
	})
	return ProtectionSnapshot{Revision: session.revision, Records: records, Digest: protectionDigest(records)}
}

func (p *ProtectionIndex) seed(records []ProtectionSeed) {
	for _, seed := range records {
		if seed.SessionID != "" {
			p.upsert(seed.SessionID, seed.Record)
		}
	}
}

func emptyProtectionSnapshot() ProtectionSnapshot {
	return ProtectionSnapshot{Digest: protectionDigest(nil), Records: []ProtectionRecord{}}
}

func (p *ProtectionIndex) observeRun(run Run) {
	if p == nil || run.SessionID == "" || run.ID == "" {
		return
	}
	record := ProtectionRecord{
		Kind: "run", ID: run.ID, RequestID: run.Metadata.RequestID,
		LegacyCreatedAt: run.CreatedAt.UnixMilli(),
	}
	if protocol.IsTerminalRunStatus(run.Status) {
		p.remove(run.SessionID, record.Kind, record.ID)
	} else {
		p.upsert(run.SessionID, record)
	}
	if !run.DurableTask {
		return
	}
	taskRecord := record
	taskRecord.Kind = "task"
	if protocol.IsTerminalRunStatus(run.Status) && run.Result != nil && protocol.IsTerminalTaskStatus(run.Result.TaskStatus) {
		p.remove(run.SessionID, taskRecord.Kind, taskRecord.ID)
	} else {
		p.upsert(run.SessionID, taskRecord)
	}
}

func (p *ProtectionIndex) observeTask(task Task) {
	if p == nil || task.SessionID == "" || task.TaskID == "" {
		return
	}
	record := ProtectionRecord{
		Kind: "task", ID: task.TaskID, LegacyCreatedAt: task.CreatedAt.UnixMilli(), StateUnknown: task.Stale,
	}
	if protocol.IsTerminalTaskStatus(task.Status) && !task.Stale {
		p.remove(task.SessionID, record.Kind, record.ID)
		return
	}
	p.upsert(task.SessionID, record)
}

func (p *ProtectionIndex) upsert(sessionID string, record ProtectionRecord) {
	key := record.Kind + "\x00" + record.ID
	for {
		current := p.state.Load()
		previous := current.sessions[sessionID]
		if existing, ok := previous.records[key]; ok {
			if record.RequestID == "" {
				record.RequestID = existing.RequestID
			}
			if record.LegacyCreatedAt == 0 {
				record.LegacyCreatedAt = existing.LegacyCreatedAt
			}
			if existing == record {
				return
			}
		}
		next := cloneProtectionState(current)
		session := next.sessions[sessionID]
		if session.records == nil {
			session.records = make(map[string]ProtectionRecord)
		}
		session.records[key] = record
		session.revision = previous.revision + 1
		next.sessions[sessionID] = session
		if p.state.CompareAndSwap(current, next) {
			return
		}
	}
}

func (p *ProtectionIndex) remove(sessionID, kind, id string) {
	key := kind + "\x00" + id
	for {
		current := p.state.Load()
		previous := current.sessions[sessionID]
		if _, ok := previous.records[key]; !ok {
			return
		}
		next := cloneProtectionState(current)
		session := next.sessions[sessionID]
		delete(session.records, key)
		session.revision = previous.revision + 1
		next.sessions[sessionID] = session
		if p.state.CompareAndSwap(current, next) {
			return
		}
	}
}

func cloneProtectionState(current *protectionState) *protectionState {
	next := &protectionState{sessions: make(map[string]protectionSession, len(current.sessions))}
	for sessionID, session := range current.sessions {
		records := make(map[string]ProtectionRecord, len(session.records))
		for key, record := range session.records {
			records[key] = record
		}
		next.sessions[sessionID] = protectionSession{revision: session.revision, records: records}
	}
	return next
}

func protectionDigest(records []ProtectionRecord) string {
	hash := sha256.New()
	writeProtectionField(hash, "half-pi:compact-protected-work:v1")
	for _, record := range records {
		writeProtectionField(hash, record.Kind)
		writeProtectionField(hash, record.ID)
		writeProtectionField(hash, record.RequestID)
		var number [8]byte
		binary.BigEndian.PutUint64(number[:], uint64(record.LegacyCreatedAt))
		_, _ = hash.Write(number[:])
		if record.StateUnknown {
			_, _ = hash.Write([]byte{1})
		} else {
			_, _ = hash.Write([]byte{0})
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func writeProtectionField(writer interface{ Write([]byte) (int, error) }, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}
