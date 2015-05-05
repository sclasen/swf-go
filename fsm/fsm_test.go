package fsm

import (
	"log"
	"strconv"
	"testing"
	"time"

	"code.google.com/p/goprotobuf/proto"
	"github.com/awslabs/aws-sdk-go/aws"
	"github.com/awslabs/aws-sdk-go/gen/swf"
	. "github.com/sclasen/swfsm/sugar"
)

//Todo add tests of error handling mechanism
//assert that the decisions have the mark and the signal external...hmm need workflow id for signal external.

var testActivityInfo = ActivityInfo{ActivityID: "activityId", ActivityType: &swf.ActivityType{Name: S("activity"), Version: S("activityVersion")}}

var typedFuncs = Typed(new(TestData))

func TestFSM(t *testing.T) {

	fsm := testFSM()

	fsm.AddInitialState(&FSMState{
		Name: "start",
		Decider: func(f *FSMContext, lastEvent swf.HistoryEvent, data interface{}) Outcome {
			testData := data.(*TestData)
			testData.States = append(testData.States, "start")
			serialized := f.Serialize(testData)
			decision := swf.Decision{
				DecisionType: S(swf.DecisionTypeScheduleActivityTask),
				ScheduleActivityTaskDecisionAttributes: &swf.ScheduleActivityTaskDecisionAttributes{
					ActivityID:   S(testActivityInfo.ActivityID),
					ActivityType: testActivityInfo.ActivityType,
					TaskList:     &swf.TaskList{Name: S("taskList")},
					Input:        S(serialized),
				},
			}

			return f.Goto("working", testData, []swf.Decision{decision})

		},
	})

	fsm.AddState(&FSMState{
		Name: "working",
		Decider: typedFuncs.Decider(func(f *FSMContext, lastEvent swf.HistoryEvent, testData *TestData) Outcome {
			testData.States = append(testData.States, "working")
			serialized := f.Serialize(testData)
			var decisions = f.EmptyDecisions()
			if *lastEvent.EventType == swf.EventTypeActivityTaskCompleted {
				decision := swf.Decision{
					DecisionType: aws.String(swf.DecisionTypeCompleteWorkflowExecution),
					CompleteWorkflowExecutionDecisionAttributes: &swf.CompleteWorkflowExecutionDecisionAttributes{
						Result: S(serialized),
					},
				}
				decisions = append(decisions, decision)
			} else if *lastEvent.EventType == swf.EventTypeActivityTaskFailed {
				decision := swf.Decision{
					DecisionType: aws.String(swf.DecisionTypeScheduleActivityTask),
					ScheduleActivityTaskDecisionAttributes: &swf.ScheduleActivityTaskDecisionAttributes{
						ActivityID:   S(testActivityInfo.ActivityID),
						ActivityType: testActivityInfo.ActivityType,
						TaskList:     &swf.TaskList{Name: S("taskList")},
						Input:        S(serialized),
					},
				}
				decisions = append(decisions, decision)
			}

			return f.Stay(testData, decisions)
		}),
	})

	events := []swf.HistoryEvent{
		swf.HistoryEvent{EventType: S("DecisionTaskStarted"), EventID: I(3)},
		swf.HistoryEvent{EventType: S("DecisionTaskScheduled"), EventID: I(2)},
		EventFromPayload(1, &swf.WorkflowExecutionStartedEventAttributes{
			Input: S(fsm.Serialize(new(TestData))),
		}),
	}

	first := testDecisionTask(0, events)

	_, decisions, _, _ := fsm.Tick(first)

	if !Find(decisions, stateMarkerPredicate) {
		t.Fatal("No Record State Marker")
	}

	if !Find(decisions, correlationMarkerPredicate) {
		t.Fatal("No Record Correlator Marker")
	}

	if !Find(decisions, scheduleActivityPredicate) {
		t.Fatal("No ScheduleActivityTask")
	}

	secondEvents := DecisionsToEvents(decisions)
	secondEvents = append(secondEvents, events...)

	if state, _ := fsm.findSerializedState(secondEvents); state.StateName != "working" {
		t.Fatal("current state is not 'working'", secondEvents)
	}

	second := testDecisionTask(3, secondEvents)

	_, secondDecisions, _, _ := fsm.Tick(second)

	if !Find(secondDecisions, stateMarkerPredicate) {
		t.Fatal("No Record State Marker")
	}

	if !Find(secondDecisions, completeWorkflowPredicate) {
		t.Fatal("No CompleteWorkflow")
	}

}

func Find(decisions []swf.Decision, predicate func(swf.Decision) bool) bool {
	return FindDecision(decisions, predicate) != nil
}

func FindDecision(decisions []swf.Decision, predicate func(swf.Decision) bool) *swf.Decision {
	for _, d := range decisions {
		if predicate(d) {
			return &d
		}
	}
	return nil
}

func stateMarkerPredicate(d swf.Decision) bool {
	return *d.DecisionType == "RecordMarker" && *d.RecordMarkerDecisionAttributes.MarkerName == StateMarker
}

func correlationMarkerPredicate(d swf.Decision) bool {
	return *d.DecisionType == "RecordMarker" && *d.RecordMarkerDecisionAttributes.MarkerName == CorrelatorMarker
}

func scheduleActivityPredicate(d swf.Decision) bool {
	return *d.DecisionType == "ScheduleActivityTask"
}

func completeWorkflowPredicate(d swf.Decision) bool {
	return *d.DecisionType == "CompleteWorkflowExecution"
}

func startTimerPredicate(d swf.Decision) bool {
	return *d.DecisionType == "StartTimer"
}

func DecisionsToEvents(decisions []swf.Decision) []swf.HistoryEvent {
	var events []swf.HistoryEvent
	for _, d := range decisions {
		if scheduleActivityPredicate(d) {
			event := swf.HistoryEvent{
				EventType: S("ActivityTaskCompleted"),
				EventID:   I(7),
				ActivityTaskCompletedEventAttributes: &swf.ActivityTaskCompletedEventAttributes{
					ScheduledEventID: I(6),
				},
			}
			events = append(events, event)
			event = swf.HistoryEvent{
				EventType: S("ActivityTaskScheduled"),
				EventID:   I(6),
				ActivityTaskScheduledEventAttributes: &swf.ActivityTaskScheduledEventAttributes{
					ActivityID:   S(testActivityInfo.ActivityID),
					ActivityType: testActivityInfo.ActivityType,
				},
			}
			events = append(events, event)
		}
		if stateMarkerPredicate(d) {
			event := swf.HistoryEvent{
				EventType: S("MarkerRecorded"),
				EventID:   I(5),
				MarkerRecordedEventAttributes: &swf.MarkerRecordedEventAttributes{
					MarkerName: S(StateMarker),
					Details:    d.RecordMarkerDecisionAttributes.Details,
				},
			}
			events = append(events, event)

		}
	}
	return events
}

type TestData struct {
	States []string
}

func TestMarshalledDecider(t *testing.T) {
	typedDecider := func(f *FSMContext, h swf.HistoryEvent, d *TestData) Outcome {
		if d.States[0] != "marshalled" {
			t.Fail()
		}
		return f.Goto("ok", d, nil)
	}

	wrapped := typedFuncs.Decider(typedDecider)

	outcome := wrapped(&FSMContext{}, swf.HistoryEvent{}, &TestData{States: []string{"marshalled"}})

	if outcome.State != "ok" {
		t.Fatal("NextState was not 'ok'")
	}
}

func TestPanicRecovery(t *testing.T) {
	s := &FSMState{
		Name: "panic",
		Decider: func(f *FSMContext, e swf.HistoryEvent, data interface{}) Outcome {
			panic("can you handle it?")
		},
	}
	f := &FSM{}
	f.AddInitialState(s)
	_, err := f.panicSafeDecide(s, new(FSMContext), swf.HistoryEvent{}, nil)
	if err == nil {
		t.Fatal("fatallz")
	} else {
		log.Println(err)
	}
}

func TestProtobufSerialization(t *testing.T) {
	ser := &ProtobufStateSerializer{}
	key := "FOO"
	val := "BAR"
	init := &ConfigVar{Key: &key, Str: &val}
	serialized, err := ser.Serialize(init)
	if err != nil {
		t.Fatal(err)
	}

	log.Println(serialized)

	deserialized := new(ConfigVar)
	err = ser.Deserialize(serialized, deserialized)
	if err != nil {
		t.Fatal(err)
	}

	if init.GetKey() != deserialized.GetKey() {
		t.Fatal(init, deserialized)
	}

	if init.GetStr() != deserialized.GetStr() {
		t.Fatal(init, deserialized)
	}
}

//This is c&p from som generated code

type ConfigVar struct {
	Key             *string `protobuf:"bytes,1,req,name=key" json:"key,omitempty"`
	Str             *string `protobuf:"bytes,2,opt,name=str" json:"str,omitempty"`
	XXXunrecognized []byte  `json:"-"`
}

func (m *ConfigVar) Reset()         { *m = ConfigVar{} }
func (m *ConfigVar) String() string { return proto.CompactTextString(m) }
func (*ConfigVar) ProtoMessage()    {}

func (m *ConfigVar) GetKey() string {
	if m != nil && m.Key != nil {
		return *m.Key
	}
	return ""
}

func (m *ConfigVar) GetStr() string {
	if m != nil && m.Str != nil {
		return *m.Str
	}
	return ""
}

func ExampleFSM() {
	// create with swf.NewClient
	var client *swf.SWF
	// data type that will be managed by the FSM
	type StateData struct {
		Message string `json:"message,omitempty"`
		Count   int    `json:"count,omitempty"`
	}
	//event type that will be signalled to the FSM with signal name "hello"
	type Hello struct {
		Message string `json:"message,omitempty"`
	}

	//This is an example of building Deciders without using decider composition.
	//the FSM we will create will oscillate between 2 states,
	//waitForSignal -> will wait till the workflow is started or signalled, and update the StateData based on the Hello message received, set a timer, and transition to waitForTimer
	//waitForTimer -> will wait till the timer set by waitForSignal fires, and will signal the workflow with a Hello message, and transition to waitFotSignal
	waitForSignal := func(f *FSMContext, h swf.HistoryEvent, d *StateData) Outcome {
		decisions := f.EmptyDecisions()
		switch *h.EventType {
		case swf.EventTypeWorkflowExecutionStarted, swf.EventTypeWorkflowExecutionSignaled:
			if *h.EventType == swf.EventTypeWorkflowExecutionSignaled && *h.WorkflowExecutionSignaledEventAttributes.SignalName == "hello" {
				hello := &Hello{}
				f.EventData(h, &Hello{})
				d.Count++
				d.Message = hello.Message
			}
			timeoutSeconds := "5" //swf uses stringy numbers in many places
			timerDecision := swf.Decision{
				DecisionType: S(swf.DecisionTypeStartTimer),
				StartTimerDecisionAttributes: &swf.StartTimerDecisionAttributes{
					StartToFireTimeout: S(timeoutSeconds),
					TimerID:            S("timeToSignal"),
				},
			}
			decisions = append(decisions, timerDecision)
			return f.Goto("waitForTimer", d, decisions)
		}
		//if the event was unexpected just stay here
		return f.Stay(d, decisions)

	}

	waitForTimer := func(f *FSMContext, h swf.HistoryEvent, d *StateData) Outcome {
		decisions := f.EmptyDecisions()
		switch *h.EventType {
		case swf.EventTypeTimerFired:
			//every time the timer fires, signal the workflow with a Hello
			message := strconv.FormatInt(time.Now().Unix(), 10)
			signalInput := &Hello{message}
			signalDecision := swf.Decision{
				DecisionType: S(swf.DecisionTypeSignalExternalWorkflowExecution),
				SignalExternalWorkflowExecutionDecisionAttributes: &swf.SignalExternalWorkflowExecutionDecisionAttributes{
					SignalName: S("hello"),
					Input:      S(f.Serialize(signalInput)),
					RunID:      f.RunID,
					WorkflowID: f.WorkflowID,
				},
			}
			decisions = append(decisions, signalDecision)

			return f.Goto("waitForSignal", d, decisions)
		}
		//if the event was unexpected just stay here
		return f.Stay(d, decisions)
	}

	//These 2 deciders are the same as the ones above, but use composable decider bits.
	typed := Typed(new(StateData))

	updateState := typed.StateFunc(func(f *FSMContext, h swf.HistoryEvent, d *StateData) {
		hello := &Hello{}
		f.EventData(h, &Hello{})
		d.Count++
		d.Message = hello.Message
	})

	setTimer := typed.DecisionFunc(func(f *FSMContext, h swf.HistoryEvent, d *StateData) swf.Decision {
		timeoutSeconds := "5" //swf uses stringy numbers in many places
		return swf.Decision{
			DecisionType: S(swf.DecisionTypeStartTimer),
			StartTimerDecisionAttributes: &swf.StartTimerDecisionAttributes{
				StartToFireTimeout: S(timeoutSeconds),
				TimerID:            S("timeToSignal"),
			},
		}
	})

	sendSignal := typed.DecisionFunc(func(f *FSMContext, h swf.HistoryEvent, d *StateData) swf.Decision {
		message := strconv.FormatInt(time.Now().Unix(), 10)
		signalInput := &Hello{message}
		return swf.Decision{
			DecisionType: S(swf.DecisionTypeSignalExternalWorkflowExecution),
			SignalExternalWorkflowExecutionDecisionAttributes: &swf.SignalExternalWorkflowExecutionDecisionAttributes{
				SignalName: S("hello"),
				Input:      S(f.Serialize(signalInput)),
				RunID:      f.RunID,
				WorkflowID: f.WorkflowID,
			},
		}
	})

	waitForSignalComposedDecider := NewComposedDecider(
		OnStarted(UpdateState(updateState), AddDecision(setTimer), Transition("waitForTimer")),
		OnSignalReceived("hello", UpdateState(updateState), AddDecision(setTimer), Transition("waitForTimer")),
		DefaultDecider(),
	)

	waitForTimerComposedDecider := NewComposedDecider(
		OnTimerFired("timeToSignal", AddDecision(sendSignal), Transition("waitForSignal")),
		DefaultDecider(),
	)

	//create the FSMState by passing the decider function through TypedDecider(),
	//which lets you use d *StateData rather than d interface{} in your decider.
	waitForSignalState := &FSMState{Name: "waitForSignal", Decider: typed.Decider(waitForSignal)}
	waitForTimerState := &FSMState{Name: "waitForTimer", Decider: typed.Decider(waitForTimer)}
	//or with the composed deciders
	waitForSignalState = &FSMState{Name: "waitForSignal", Decider: waitForSignalComposedDecider}
	waitForTimerState = &FSMState{Name: "waitForTimer", Decider: waitForTimerComposedDecider}
	//wire it up in an fsm
	fsm := &FSM{
		Name:       "example-fsm",
		SWF:        client,
		DataType:   StateData{},
		Domain:     "exaple-swf-domain",
		TaskList:   "example-decision-task-list-to-poll",
		Serializer: &JSONStateSerializer{},
	}
	//add states to FSM
	fsm.AddInitialState(waitForSignalState)
	fsm.AddState(waitForTimerState)

	//start it up!
	fsm.Start()

	//To start workflows using this fsm
	client.StartWorkflowExecution(&swf.StartWorkflowExecutionInput{
		Domain:     S("exaple-swf-domain"),
		WorkflowID: S("your-id"),
		//you will have previously regiestered a WorkflowType that this FSM will work.
		WorkflowType: &swf.WorkflowType{Name: S("the-name"), Version: S("the-version")},
		Input:        S(fsm.Serialize(&StateData{Count: 0, Message: "starting message"})),
	})
}

func TestContinuedWorkflows(t *testing.T) {
	fsm := testFSM()

	fsm.AddInitialState(&FSMState{
		Name: "ok",
		Decider: func(f *FSMContext, h swf.HistoryEvent, d interface{}) Outcome {
			if *h.EventType == swf.EventTypeWorkflowExecutionStarted && d.(*TestData).States[0] == "continuing" {
				return f.Stay(d, nil)
			}
			panic("broken")
		},
	})

	stateData := fsm.Serialize(TestData{States: []string{"continuing"}})
	state := SerializedState{
		StateVersion: 23,
		StateName:    "ok",
		StateData:    stateData,
	}

	serializedState := fsm.Serialize(state)
	resp := testDecisionTask(4, []swf.HistoryEvent{swf.HistoryEvent{
		EventType: S(swf.EventTypeWorkflowExecutionStarted),
		WorkflowExecutionStartedEventAttributes: &swf.WorkflowExecutionStartedEventAttributes{
			Input: S(serializedState),
			ContinuedExecutionRunID: S("someRunId"),
		},
	}})

	log.Printf("%+v", resp)
	_, decisions, updatedState, _ := fsm.Tick(resp)

	log.Println(updatedState)

	if updatedState.StateVersion != 24 {
		t.Fatal("StateVersion !=24 ", updatedState.StateVersion)
	}

	if len(decisions) != 1 && *decisions[0].RecordMarkerDecisionAttributes.MarkerName != StateMarker {
		t.Fatal("unexpected decisions")
	}
}

func TestContinueWorkflowDecision(t *testing.T) {

	fsm := testFSM()
	ctx := testContext(fsm)
	ctx.stateVersion = uint64(7)
	ctx.stateData = &TestData{States: []string{"continuing"}}

	fsm.AddInitialState(&FSMState{
		Name: "InitialState",
		Decider: func(ctx *FSMContext, h swf.HistoryEvent, data interface{}) Outcome {
			return ctx.Pass()
		},
	},
	)

	cont := ctx.ContinueWorkflowDecision("InitialState", ctx.stateData)
	testData := new(TestData)
	serState := new(SerializedState)
	ctx.Deserialize(*cont.ContinueAsNewWorkflowExecutionDecisionAttributes.Input, serState)
	ctx.Deserialize(serState.StateData, testData)
	if len(testData.States) != 1 || testData.States[0] != "continuing" || serState.StateVersion != 7 || serState.StateName != "InitialState" {
		t.Fatal(testData, cont)
	}

}

func TestCompleteState(t *testing.T) {
	fsm := testFSM()

	ctx := testContext(fsm)

	event := swf.HistoryEvent{
		EventID:   I(1),
		EventType: S("WorkflowExecutionStarted"),
		WorkflowExecutionStartedEventAttributes: &swf.WorkflowExecutionStartedEventAttributes{
			Input: S(fsm.Serialize(new(TestData))),
		},
	}

	fsm.AddInitialState(fsm.DefaultCompleteState())
	fsm.Init()
	outcome := fsm.completeState.Decider(ctx, event, new(TestData))

	if len(outcome.Decisions) != 1 {
		t.Fatal(outcome)
	}

	if *outcome.Decisions[0].DecisionType != swf.DecisionTypeCompleteWorkflowExecution {
		t.Fatal(outcome)
	}
}

func TestEntryDecisions(t *testing.T) {
	fsm := testFSM()

	event := swf.HistoryEvent{
		EventID:   I(1),
		EventType: S("WorkflowExecutionStarted"),
		WorkflowExecutionStartedEventAttributes: &swf.WorkflowExecutionStartedEventAttributes{
			Input: S(fsm.Serialize(new(TestData))),
		},
	}

	entryDecisions := func(ctx *FSMContext, h swf.HistoryEvent, data interface{}) []swf.Decision {
		t.Logf("IN ENTRY DECISIONS")
		return []swf.Decision{swf.Decision{
			DecisionType: S(swf.DecisionTypeScheduleActivityTask),
			ScheduleActivityTaskDecisionAttributes: &swf.ScheduleActivityTaskDecisionAttributes{
				ActivityID:   S(testActivityInfo.ActivityID),
				ActivityType: testActivityInfo.ActivityType,
				TaskList:     &swf.TaskList{Name: S("taskList")},
				Input:        S(""),
			},
		}}
	}

	fsm.AddInitialState(&FSMState{
		Name: "InitialState",
		Decider: func(ctx *FSMContext, h swf.HistoryEvent, data interface{}) Outcome {
			t.Logf("IN INITIAL STATE")
			return ctx.Goto("SecondState", data, ctx.EmptyDecisions())
		},
	})

	fsm.AddState(&FSMState{
		Name: "SecondState",
		Decider: func(ctx *FSMContext, h swf.HistoryEvent, data interface{}) Outcome {
			t.Logf("IN SECOND STATE")
			return ctx.Stay(data, ctx.EmptyDecisions())
		},
		EntryDecisions: entryDecisions,
	})

	fsm.Init()

	_, decisions, state, _ := fsm.Tick(
		&swf.DecisionTask{
			Events:                 []swf.HistoryEvent{event},
			PreviousStartedEventID: L(0),
			StartedEventID:         L(1),

			TaskToken: S("token"),
			WorkflowExecution: &swf.WorkflowExecution{
				WorkflowID: S("fii"),
				RunID:      S("run"),
			},
			WorkflowType: &swf.WorkflowType{
				Name:    S("foo"),
				Version: S("1"),
			},
		},
	)

	if len(decisions) != 3 {
		//2 state markers + 1 EntryDecision
		t.Fatal("Not one decision", len(decisions))
	}

	for _, d := range decisions {
		if *d.DecisionType == swf.DecisionTypeRecordMarker {
			continue
		}
		if *d.DecisionType != swf.DecisionTypeScheduleActivityTask {
			t.Fatal("not activity")
		}
	}

	if state.StateName != "SecondState" {
		t.Fatal("not SecondState", state.StateName)
	}
}

func testFSM() *FSM {
	fsm := &FSM{
		Name:             "test-fsm",
		DataType:         TestData{},
		Serializer:       JSONStateSerializer{},
		systemSerializer: JSONStateSerializer{},
		allowPanics:      true,
	}
	return fsm
}

func testContext(fsm *FSM) *FSMContext {
	return NewFSMContext(
		fsm,
		swf.WorkflowType{Name: S("test-workflow"), Version: S("1")},
		swf.WorkflowExecution{WorkflowID: S("test-workflow-1"), RunID: S("123123")},
		&EventCorrelator{},
		"InitialState", &TestData{}, 0,
	)
}

func testDecisionTask(prevStarted int, events []swf.HistoryEvent) *swf.DecisionTask {

	d := &swf.DecisionTask{
		Events:                 events,
		PreviousStartedEventID: I(prevStarted),
		StartedEventID:         I(prevStarted + len(events)),
		WorkflowExecution:      testWorkflowExecution,
		WorkflowType:           testWorkflowType,
	}
	for i, e := range d.Events {
		if e.EventID == nil {
			e.EventID = L(*d.StartedEventID - int64(i))
		}
		e.EventTimestamp = &aws.UnixTimestamp{time.Unix(0, 0)}
		d.Events[i] = e
	}
	return d
}

func testHistoryEvent(eventID int, eventType string) swf.HistoryEvent {
	return swf.HistoryEvent{
		EventID:   I(eventID),
		EventType: S(eventType),
	}
}

var testWorkflowExecution = &swf.WorkflowExecution{WorkflowID: S("workflow-id"), RunID: S("run-id")}
var testWorkflowType = &swf.WorkflowType{Name: S("workflow-name"), Version: S("workflow-version")}
