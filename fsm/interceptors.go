package fsm

import (
	"github.com/awslabs/aws-sdk-go/service/swf"
)

//DecisionInterceptor allows manipulation of the decision task and the outcome at key points in the task lifecycle.
type DecisionInterceptor interface {
	BeforeTask(decision *swf.PollForDecisionTaskOutput)
	BeforeDecision(decision *swf.PollForDecisionTaskOutput, ctx *FSMContext, outcome *Outcome)
	AfterDecision(decision *swf.PollForDecisionTaskOutput, ctx *FSMContext, outcome *Outcome)
}

//FuncInterceptor is a DecisionInterceptor that you can set handler funcs on. if any are unset, they are no-ops.
type FuncInterceptor struct {
	BeforeTaskFn     func(decision *swf.PollForDecisionTaskOutput)
	BeforeDecisionFn func(decision *swf.PollForDecisionTaskOutput, ctx *FSMContext, outcome *Outcome)
	AfterDecisionFn  func(decision *swf.PollForDecisionTaskOutput, ctx *FSMContext, outcome *Outcome)
}

//BeforeTask runs the BeforeTaskFn if not nil
func (i *FuncInterceptor) BeforeTask(decision *swf.PollForDecisionTaskOutput) {
	if i.BeforeTaskFn != nil {
		i.BeforeTaskFn(decision)
	}
}

//BeforeDecision runs the BeforeDecisionFn if not nil
func (i *FuncInterceptor) BeforeDecision(decision *swf.PollForDecisionTaskOutput, ctx *FSMContext, outcome *Outcome) {
	if i.BeforeDecisionFn != nil {
		i.BeforeDecisionFn(decision, ctx, outcome)
	}
}

//AfterDecision runs the AfterDecisionFn if not nil
func (i *FuncInterceptor) AfterDecision(decision *swf.PollForDecisionTaskOutput, ctx *FSMContext, outcome *Outcome) {
	if i.AfterDecisionFn != nil {
		i.AfterDecisionFn(decision, ctx, outcome)
	}
}
