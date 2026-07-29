package orch

import (
	"sync"
	"sync/atomic"

	"github.com/lansonsam/buslab/internal/model"
)

type EventKind int

const (
	EventBusCreated EventKind = iota
	EventBusChanged
	EventBusDeleted
	EventNodeChanged
	EventNodeDeleted
	EventFrame
	EventError
	EventHostStatus
	EventProjectReplaced
)

func (k EventKind) String() string {
	switch k {
	case EventBusCreated:
		return "BusCreated"
	case EventBusChanged:
		return "BusChanged"
	case EventBusDeleted:
		return "BusDeleted"
	case EventNodeChanged:
		return "NodeChanged"
	case EventNodeDeleted:
		return "NodeDeleted"
	case EventFrame:
		return "Frame"
	case EventError:
		return "Error"
	case EventHostStatus:
		return "HostStatus"
	case EventProjectReplaced:
		return "ProjectReplaced"
	}
	return "Unknown"
}

type Event struct {
	Kind    EventKind
	Bus     model.BusID
	Node    model.NodeID
	Frame   model.Frame
	Message string
	Err     error
}

// broker 向订阅者广播事件；订阅者来不及消费时丢弃并计数，避免阻塞 Adapter。
type broker struct {
	mu      sync.Mutex
	subs    map[int]chan Event
	nextID  int
	dropped atomic.Uint64
}

func newBroker() *broker { return &broker{subs: map[int]chan Event{}} }

func (b *broker) subscribe(buffer int) (<-chan Event, func()) {
	if buffer <= 0 {
		buffer = 1024
	}
	ch := make(chan Event, buffer)
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subs[id] = ch
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		if sub, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(sub)
		}
		b.mu.Unlock()
	}
}

func (b *broker) publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			b.dropped.Add(1)
		}
	}
}

func (b *broker) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, ch := range b.subs {
		delete(b.subs, id)
		close(ch)
	}
}

func (b *broker) droppedCount() uint64 { return b.dropped.Load() }
