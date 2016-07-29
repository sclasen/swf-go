package poller

import (
	"sync"
	"time"

	"golang.org/x/net/context"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/swf"
	"github.com/juju/errors"
	"github.com/pborman/uuid"
	. "github.com/sclasen/swfsm/log"
	. "github.com/sclasen/swfsm/sugar"
)

// SWFOps is the subset of the swf.SWF api used by pollers
type DecisionOps interface {
	PollForDecisionTaskPages(*swf.PollForDecisionTaskInput, func(*swf.PollForDecisionTaskOutput, bool) bool) error
}

type ActivityOps interface {
	PollForActivityTask(req *swf.PollForActivityTaskInput) (resp *swf.PollForActivityTaskOutput, err error)
}

// NewDecisionTaskPoller returns a DecisionTaskPoller whick can be used to poll the given task list.
func NewDecisionTaskPoller(dwc DecisionOps, domain string, identity string, taskList string) *DecisionTaskPoller {
	return &DecisionTaskPoller{
		client:   dwc,
		Domain:   domain,
		Identity: identity,
		TaskList: taskList,
	}
}

// DecisionTaskPoller polls a given task list in a domain for decision tasks.
type DecisionTaskPoller struct {
	client   DecisionOps
	Identity string
	Domain   string
	TaskList string
}

// Poll polls the task list for a task. If there is no task available, nil is
// returned. If an error is encountered, no task is returned.
func (p *DecisionTaskPoller) Poll(taskReady func(*swf.PollForDecisionTaskOutput) bool) (context.Context, *swf.PollForDecisionTaskOutput, error) {
	var (
		resp   *swf.PollForDecisionTaskOutput
		page   int
		pollId = uuid.New()
		ctx    = context.WithValue(context.Background(), "requestID", pollId)
	)

	eachPage := func(out *swf.PollForDecisionTaskOutput, _ bool) bool {
		page++

		var (
			firstEventId *int64
			lastEventId  *int64
			workflowId   string
		)

		if len(out.Events) > 0 {
			firstEventId = out.Events[0].EventId
			lastEventId = out.Events[len(out.Events)-1].EventId
		}

		if out.WorkflowExecution != nil {
			workflowId = LS(out.WorkflowExecution.WorkflowId)
		} else {
			workflowId = "no-workflow-execution"
		}

		Log.Printf("component=DecisionTaskPoller at=decision-task-page poll-id=%q task-list=%q workflow=%q page=%d "+
			"PreviousStartedEventId=%s StartedEventId=%s NumEvents=%d FirstEventId=%s LastEventId=%s",
			pollId, p.TaskList, workflowId, page,
			LL(out.PreviousStartedEventId), LL(out.StartedEventId), len(out.Events), LL(firstEventId), LL(lastEventId))

		if resp == nil {
			resp = out
		} else {
			resp.Events = append(resp.Events, out.Events...)
		}

		return !taskReady(resp)
	}

	err := p.client.PollForDecisionTaskPages(&swf.PollForDecisionTaskInput{
		Domain:       aws.String(p.Domain),
		Identity:     aws.String(p.Identity),
		ReverseOrder: aws.Bool(true),
		TaskList:     &swf.TaskList{Name: aws.String(p.TaskList)},
	}, eachPage)

	if err != nil {
		Log.Printf("component=DecisionTaskPoller poll-id=%q task-list=%q at=error error=%q",
			pollId, p.TaskList, err.Error())
		return nil, nil, errors.Trace(err)
	}
	if resp != nil && resp.TaskToken != nil {
		Log.Printf("component=DecisionTaskPoller poll-id=%q at=decision-task-received task-list=%q workflow=%q",
			pollId, p.TaskList, LS(resp.WorkflowExecution.WorkflowId))
		p.logTaskLatency(resp)
		return ctx, resp, nil
	}
	Log.Printf("component=DecisionTaskPoller at=decision-task-empty-response poll-id=%q task-list=%q", pollId, p.TaskList)
	return nil, nil, nil
}

// PollUntilShutdownBy will poll until signaled to shutdown by the PollerShutdownManager. this func blocks, so run it in a goroutine if necessary.
// The implementation calls Poll() and invokes the callback whenever a valid PollForDecisionTaskResponse is received.
func (p *DecisionTaskPoller) PollUntilShutdownBy(mgr *ShutdownManager, pollerName string, onTask func(context.Context, *swf.PollForDecisionTaskOutput), taskReady func(*swf.PollForDecisionTaskOutput) bool) {
	stop := make(chan bool, 1)
	stopAck := make(chan bool, 1)
	mgr.Register(pollerName, stop, stopAck)
	for {
		select {
		case <-stop:
			Log.Printf("component=DecisionTaskPoller fn=PollUntilShutdownBy at=received-stop action=shutting-down poller=%s task-list=%q", pollerName, p.TaskList)
			stopAck <- true
			return
		default:
			ctx, task, err := p.Poll(taskReady)
			if err != nil {
				Log.Printf("component=DecisionTaskPoller fn=PollUntilShutdownBy at=poll-err poller=%s task-list=%q error=%q", pollerName, p.TaskList, err)
				continue
			}
			if task == nil {
				Log.Printf("component=DecisionTaskPoller fn=PollUntilShutdownBy at=poll-no-task poller=%s task-list=%q", pollerName, p.TaskList)
				continue
			}
			onTask(ctx, task)
		}
	}
}

func (p *DecisionTaskPoller) logTaskLatency(resp *swf.PollForDecisionTaskOutput) {
	for _, e := range resp.Events {
		if e.EventId == resp.StartedEventId {
			elapsed := time.Since(*e.EventTimestamp)
			Log.Printf("component=DecisionTaskPoller at=decision-task-latency latency=%s workflow=%s", elapsed, LS(resp.WorkflowType.Name))
		}
	}
}

// NewActivityTaskPoller returns an ActivityTaskPoller.
func NewActivityTaskPoller(awc ActivityOps, domain string, identity string, taskList string) *ActivityTaskPoller {
	return &ActivityTaskPoller{
		client:   awc,
		Domain:   domain,
		Identity: identity,
		TaskList: taskList,
	}
}

// ActivityTaskPoller polls a given task list in a domain for activity tasks, and sends tasks on its Tasks channel.
type ActivityTaskPoller struct {
	client   ActivityOps
	Identity string
	Domain   string
	TaskList string
}

// Poll polls the task list for a task. If there is no task, nil is returned.
// If an error is encountered, no task is returned.
func (p *ActivityTaskPoller) Poll() (context.Context, *swf.PollForActivityTaskOutput, error) {
	pollID := uuid.New()
	ctx := context.WithValue(context.Background(), "requestID", pollID)

	resp, err := p.client.PollForActivityTask(&swf.PollForActivityTaskInput{
		Domain:   aws.String(p.Domain),
		Identity: aws.String(p.Identity),
		TaskList: &swf.TaskList{Name: aws.String(p.TaskList)},
	})
	if err != nil {
		Log.Printf("component=ActivityTaskPoller poll-id=%s at=error error=%q", pollID, err.Error())
		return nil, nil, errors.Trace(err)
	}
	if resp.TaskToken != nil {
		Log.Printf("component=ActivityTaskPoller at=activity-task-received poll-id=%s activity=%s", pollID, LS(resp.ActivityType.Name))
		return ctx, resp, nil
	}
	Log.Printf("component=ActivityTaskPoller at=activity-task-empty-response poll-id=%s", pollID)
	return nil, nil, nil
}

// PollUntilShutdownBy will poll until signaled to shutdown by the ShutdownManager. this func blocks, so run it in a goroutine if necessary.
// The implementation calls Poll() and invokes the callback whenever a valid PollForActivityTaskResponse is received.
func (p *ActivityTaskPoller) PollUntilShutdownBy(mgr *ShutdownManager, pollerName string, onTask func(context.Context, *swf.PollForActivityTaskOutput)) {
	stop := make(chan bool, 1)
	stopAck := make(chan bool, 1)
	mgr.Register(pollerName, stop, stopAck)
	for {
		select {
		case <-stop:
			Log.Printf("component=ActivityTaskPoller fn=PollUntilShutdownBy at=received-stop action=shutting-down poller=%s task-list=%q", pollerName, p.TaskList)
			stopAck <- true
			return
		default:
			ctx, task, err := p.Poll()
			if err != nil {
				Log.Printf("component=ActivityTaskPoller fn=PollUntilShutdownBy at=poll-err poller=%s task-list=%q error=%q", pollerName, p.TaskList, err)
				continue
			}
			if task == nil {
				Log.Printf("component=ActivityTaskPoller fn=PollUntilShutdownBy at=poll-no-task poller=%s task-list=%q", pollerName, p.TaskList)
				continue
			}
			onTask(ctx, task)
		}
	}
}

// ShutdownManager facilitates cleanly shutting down pollers when the application decides to exit. When StopPollers() is called it will
// send to each of the stopChan that have been registered, then recieve from each of the ackChan that have been registered. At this point StopPollers() returns.
type ShutdownManager struct {
	rpMu              sync.Mutex // protects registeredPollers
	registeredPollers map[string]*registeredPoller
}

type registeredPoller struct {
	name           string
	stopChannel    chan bool
	stopAckChannel chan bool
}

// NewShutdownManager creates a ShutdownManager
func NewShutdownManager() *ShutdownManager {

	mgr := &ShutdownManager{
		registeredPollers: make(map[string]*registeredPoller),
	}

	return mgr

}

//StopPollers blocks until it is able to stop all the registered pollers, which can take up to 60 seconds.
//the registered pollers are cleared once all pollers have acked the stop.
func (p *ShutdownManager) StopPollers() {
	p.rpMu.Lock()
	defer p.rpMu.Unlock()

	Log.Printf("component=PollerShutdownManager at=stop-pollers")
	for _, r := range p.registeredPollers {
		Log.Printf("component=PollerShutdownManager at=sending-stop name=%s", r.name)
		r.stopChannel <- true
	}
	for _, r := range p.registeredPollers {
		Log.Printf("component=PollerShutdownManager at=awaiting-stop-ack name=%s", r.name)
		<-r.stopAckChannel
		Log.Printf("component=PollerShutdownManager at=stop-ack name=%s", r.name)
	}
	p.registeredPollers = map[string]*registeredPoller{}
}

// Register registers a named pair of channels to the shutdown manager. Buffered channels please!
func (p *ShutdownManager) Register(name string, stopChan chan bool, ackChan chan bool) {
	p.rpMu.Lock()
	defer p.rpMu.Unlock()
	p.registeredPollers[name] = &registeredPoller{name, stopChan, ackChan}
}

// Deregister removes a registered pair of channels from the shutdown manager.
func (p *ShutdownManager) Deregister(name string) {
	p.rpMu.Lock()
	defer p.rpMu.Unlock()
	delete(p.registeredPollers, name)
}
