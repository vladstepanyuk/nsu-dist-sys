package common

import (
	"context"
	"sync/atomic"
)

type Actor interface {
	ProcessMessage(req any)
}

const (
	EXECUTOR_STATE_STARTING = iota
	EXECUTOR_STATE_RUNNING
	EXECUTOR_STATE_STOPPING
	EXECUTOR_STATE_STOPPED
)

type ActorExecutor struct {
	actor    Actor
	state    atomic.Int32
	requests chan any
	stopReq  chan struct{}
}

func NewActorExecutor(actor Actor) *ActorExecutor {

	exec := &ActorExecutor{
		actor:    actor,
		state:    atomic.Int32{},
		requests: make(chan any, 100),
		stopReq:  make(chan struct{}),
	}

	exec.state.Store(EXECUTOR_STATE_STOPPED)
	return exec
}

func (ae *ActorExecutor) Start() (ok bool) {

	swapped := ae.state.CompareAndSwap(EXECUTOR_STATE_STOPPED, EXECUTOR_STATE_STARTING)
	if !swapped {
		return false
	}

	go ae.doWork()
	return true
}

func (ae *ActorExecutor) Stop(ctx context.Context) (ok bool) {

	swapped := ae.state.CompareAndSwap(EXECUTOR_STATE_RUNNING, EXECUTOR_STATE_STOPPING)
	if !swapped {
		return false
	}

	select {
	case ae.stopReq <- struct{}{}:
		return true
	case <-ctx.Done():
		ae.state.Store(EXECUTOR_STATE_RUNNING)
		return false
	}
}

func (ae *ActorExecutor) SendReq(ctx context.Context, req any) (ok bool) {
	select {
	case ae.requests <- req:
		return true
	case <-ctx.Done():
		return false
	}
}

func (ae *ActorExecutor) doWork() {
	ae.state.Store(EXECUTOR_STATE_RUNNING)

	for {
		select {
		case <-ae.stopReq:
			ae.state.Store(EXECUTOR_STATE_STOPPED)
			return
		case req := <-ae.requests:
			ae.actor.ProcessMessage(req)
		}

	}
}
