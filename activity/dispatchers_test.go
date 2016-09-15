package activity

import (
	"sync/atomic"
	"testing"

	"golang.org/x/net/context"

	"github.com/aws/aws-sdk-go/service/swf"

	"time"
)

func TestCallingGoroutineDispatcher(t *testing.T) {
	testDispatcher(&CallingGoroutineDispatcher{}, t)
}

func TestNewGoroutineDispatcher(t *testing.T) {
	testDispatcher(&NewGoroutineDispatcher{}, t)
}
func TestBoundedGoroutineDispatcher(t *testing.T) {
	testDispatcher(&BoundedGoroutineDispatcher{NumGoroutines: 8}, t)
}

func TestCountdownGoroutineDispatcher(t *testing.T) {
	dispatcher := &CountdownGoroutineDispatcher{
		Stop:    make(chan bool, 1),
		StopAck: make(chan bool, 1),
	}

	go dispatcher.Start()

	go func() {
		testDispatcher(dispatcher, t)
		dispatcher.Stop <- true
	}()

	select {
	case <-dispatcher.StopAck:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for tasks to countdown")
	}
}

func testDispatcher(dispatcher ActivityTaskDispatcher, t *testing.T) {
	task := &swf.PollForActivityTaskOutput{}
	tasksHandled := int32(0)
	totalTasks := int32(1000)
	done := make(chan struct{}, 1)
	handler := func(ctx context.Context, d *swf.PollForActivityTaskOutput) {
		handled := atomic.AddInt32(&tasksHandled, 1)
		if handled == totalTasks {
			done <- struct{}{}
		}
	}

	for i := int32(0); i < totalTasks; i++ {
		dispatcher.DispatchTask(context.Background(), task, handler)
	}

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for tasks. Only completed:", tasksHandled)
	}
}
