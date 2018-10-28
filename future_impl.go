package actorkit

import (
	"sync"
	"time"

	"github.com/gokit/es"

	"github.com/gokit/errors"
	"github.com/gokit/xid"
)

// errors ...
var (
	ErrFutureTimeout          = errors.New("Future timed out")
	ErrFutureResolved         = errors.New("Future is resolved")
	ErrFutureEscalatedFailure = errors.New("Future failed due to escalated error")
)

// FutureImpl defines an implementation for the Future actor type.
type FutureImpl struct {
	id     xid.ID
	parent Addr
	timer  *time.Timer
	events *es.EventStream

	ac    sync.Mutex
	pipes []Addr

	w      sync.WaitGroup
	cw     sync.Mutex
	err    error
	result *Envelope
}

// NewFuture returns a new instance of giving future.
func NewFuture(parent Addr) *FutureImpl {
	var ft FutureImpl
	ft.parent = parent
	ft.events = es.New()
	ft.w.Add(1)
	return &ft
}

// TimedFuture returns a new instance of giving future.
func TimedFuture(parent Addr, dur time.Duration) *FutureImpl {
	var ft FutureImpl
	ft.parent = parent
	ft.events = es.New()
	ft.timer = time.NewTimer(dur)
	ft.w.Add(1)

	go ft.timedResolved()
	return &ft
}

// Forward delivers giving envelope into Future actor which if giving
// future is not yet resolved will be the resolution of future.
func (f *FutureImpl) Forward(reply Envelope) error {
	if f.resolved() {
		return errors.Wrap(ErrFutureResolved, "Future %q already resolved", f.Addr())
	}

	f.Resolve(reply)
	f.broadcast()
	return nil
}

// Send delivers giving data to Future as the resolution of
// said Future. The data provided will be used as the resolved
// value of giving future, if it's not already resolved.
func (f *FutureImpl) Send(data interface{}, h Header, addr Addr) error {
	if f.resolved() {
		return errors.Wrap(ErrFutureResolved, "Future %q already resolved", f.Addr())
	}

	f.Resolve(CreateEnvelope(addr, h, data))
	f.broadcast()
	return nil
}

// Escalate escalates giving value into the parent of giving future, which also fails future
// and resolves it as a failure.
func (f *FutureImpl) Escalate(m interface{}) {
	if merr, ok := m.(error); ok {
		f.Reject(merr)
	} else {
		f.Reject(ErrFutureEscalatedFailure)
	}

	f.broadcastError()
}

// Future returns a new future instance from giving source.
func (f *FutureImpl) Future() Future {
	return NewFuture(f.parent)
}

// TimedFuture returns a new future instance from giving source.
func (f *FutureImpl) TimedFuture(d time.Duration) Future {
	return TimedFuture(f.parent, d)
}

// Watch adds giving function into event system for future.
func (f *FutureImpl) Watch(fn func(interface{})) Subscription {
	return f.events.Subscribe(fn)
}

// Parent returns the address of the parent of giving Future.
func (f *FutureImpl) Parent() Addr {
	return f.parent
}

// Ancestor returns the root parent address of the of giving Future.
func (f *FutureImpl) Ancestor() Addr {
	return f.parent.Ancestor()
}

// Children returns an empty slice as futures can not have children actors.
func (f *FutureImpl) Children() []Addr {
	return nil
}

// Service returns the "Future" as the service name of FutureImpl.
func (f *FutureImpl) Service() string {
	return "Future"
}

// ID returns the unique id of giving Future.
func (f *FutureImpl) ID() string {
	return f.id.String()
}

// Addr returns s consistent address format representing a future which will always be
// in the format:
//
//	ParentAddr/:future/FutureID
//
func (f *FutureImpl) Addr() string {
	return f.parent.Addr() + "/:future/" + f.id.String()
}

// AddressOf requests giving service from future's parent AddressOf method.
func (f *FutureImpl) AddressOf(service string, ancestry bool) (Addr, error) {
	return f.parent.AddressOf(service, ancestry)
}

// Spawn requests giving service and Receiver from future's parent Spawn method.
func (f *FutureImpl) Spawn(service string, rr Behaviour, initial interface{}) (Addr, error) {
	return f.parent.Spawn(service, rr, initial)
}

// Wait blocks till the giving future is resolved and returns error if
// occurred.
func (f *FutureImpl) Wait() error {
	var err error
	f.w.Wait()
	f.cw.Lock()
	err = f.err
	f.cw.Unlock()
	return err
}

// Pipe adds giving set of address into giving Future.
func (f *FutureImpl) Pipe(addrs ...Addr) {
	if f.resolved() {
		for _, addr := range addrs {
			addr.Forward(*f.result)
		}
		return
	}

	f.ac.Lock()
	f.pipes = append(f.pipes, addrs...)
	f.ac.Unlock()
}

// Err returns the error for the failure of
// giving error.
func (f *FutureImpl) Err() error {
	f.cw.Lock()
	defer f.cw.Unlock()
	return f.err
}

// Result returns the envelope which is used to resolve the future.
func (f *FutureImpl) Result() Envelope {
	f.cw.Lock()
	defer f.cw.Unlock()
	return *f.result
}

// Resolve resolves giving future with envelope.
func (f *FutureImpl) Resolve(env Envelope) {
	if f.resolved() {
		return
	}

	f.cw.Lock()
	f.result = &env
	f.cw.Unlock()
	f.w.Done()

	f.events.Publish(FutureResolved{Data: env, ID: f.id.String()})
}

// Reject rejects giving future with error.
func (f *FutureImpl) Reject(err error) {
	if f.resolved() {
		return
	}

	f.cw.Lock()
	f.err = err
	f.cw.Unlock()
	f.w.Done()

	f.events.Publish(FutureRejected{Error: err, ID: f.id.String()})
}

func (f *FutureImpl) resolved() bool {
	f.cw.Lock()
	defer f.cw.Unlock()
	return f.result != nil || f.err != nil
}

func (f *FutureImpl) broadcastError() {
	var res error

	f.ac.Lock()
	res = f.err
	f.ac.Unlock()

	for _, addr := range f.pipes {
		if rr, ok := addr.(Resolvables); ok {
			rr.Reject(res)
		}
	}
}

func (f *FutureImpl) broadcast() {
	var res *Envelope

	f.ac.Lock()
	res = f.result
	f.ac.Unlock()

	for _, addr := range f.pipes {
		addr.Forward(*res)
	}
}

func (f *FutureImpl) timedResolved() {
	<-f.timer.C
	if f.resolved() {
		return
	}
	f.Reject(ErrFutureTimeout)
}
