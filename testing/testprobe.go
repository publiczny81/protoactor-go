package testactor

import (
	"fmt"
	"github.com/AsynkronIT/protoactor-go/actor"
	"github.com/stretchr/testify/suite"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type Expected []interface{}

func (e Expected) String() string {
	builder := &strings.Builder{}
	builder.WriteRune('[')
	for i, v := range e {

		builder.WriteString(fmt.Sprintf("%d: %v\n", i, v))

		if i < len(e)-1 {
			builder.WriteRune(' ')
		}
	}
	builder.WriteRune(']')
	return builder.String()
}

// message ends processing expected messages successfully
type success struct{}

// message ends processing expected messages with failure
type failure struct {
	message string
}

type testProbe struct {
	pid     *actor.PID
	t       *testing.T
	system  *actor.ActorSystem
	message chan interface{}
	timeout time.Duration
}

func (p testProbe) WithTimeout(duration time.Duration) testProbe {
	p.timeout = duration
	return p
}

func (p testProbe) WithinSuite(s interface{}) testProbe {
	if s == nil {
		panic("called with nil")
	}
	if suite, ok := s.(*suite.Suite); ok {
		p.t = suite.T()
	} else {
		panic(fmt.Sprintf("Invalid argument type %v", reflect.TypeOf(s)))
	}

	return p
}

// Creates a test probe within given test and set timeout to default value 1 minute.
//If context is provided it will be used to spawn new test probe, otherwise new root context will be created
func ProbeWithinTest(t *testing.T, system *actor.ActorSystem) *testProbe {
	if t == nil {
		panic("t cannot be nil")
	}

	if system == nil {
		panic("system cannot be nil")
	}

	probe := &testProbe{t: t, system: system, message: make(chan interface{}, 3), timeout: time.Minute}
	probe.pid = system.Root.Spawn(actor.PropsFromProducer(func() actor.Actor {
		return probe
	}))

	return probe
}

type requireSuite interface {
	T() *testing.T
}

// Creates a test probe within given suite and set timeout to default value 1 minute.
//If context is provided it will be used to spawn new test probe, otherwise new root context will be created
func ProbeWithinSuite(s interface{}, system *actor.ActorSystem) *testProbe {
	if s == nil {
		panic("suite cannot be nil")
	}

	if s, ok := s.(requireSuite); ok {
		return ProbeWithinTest(s.T(), system)
	}
	panic("s must be a suite")
}

func (a *testProbe) Receive(ctx actor.Context) {
	switch msg := ctx.Message().(type) {
	case actor.SystemMessage, actor.AutoReceiveMessage:
		//ignore
	case *success, *failure:
		close(a.message)
	default:
		a.message <- msg
	}
}

func (p *testProbe) PID() *actor.PID {
	return p.pid
}

// Interrupts all messages send by actor under test
func (p *testProbe) AsSenderMiddleware() actor.SenderMiddleware {
	return func(next actor.SenderFunc) actor.SenderFunc {
		return func(c actor.SenderContext, target *actor.PID, envelope *actor.MessageEnvelope) {
			if target != p.pid {
				p.message <- envelope.Message
			}
			next(c, target, envelope)
		}
	}
}

func (p *testProbe) AsProps() *actor.Props {
	return actor.PropsFromFunc(func(c actor.Context) {
		switch c.Message().(type) {
		case actor.SystemMessage:
		//ignore
		case actor.AutoReceiveMessage:
		//ignore
		default:
			c.Forward(p.pid)
		}
	})
}

/*
Sends a message to actor
*/
func (a *testProbe) Send(pid *actor.PID, message interface{}) {
	a.system.Root.Send(pid, message)
}

/*
Sends a request to actor
*/
func (a *testProbe) Request(pid *actor.PID, message interface{}) {
	a.system.Root.RequestWithCustomSender(pid, message, a.pid)
}

/*
Verifies if all expected user defined messages have been received. The order of messages is not checked
*/
func (a *testProbe) ExpectUnorderedMessages(msg ...interface{}) {

	var (
		mutex            sync.Mutex
		expectedMessages Expected = msg
		receivedMessages          = make(Expected, 0)
		done                      = make(chan interface{}, 1)
	)

	go func() {
		var (
			remainMessages = make([]interface{}, len(expectedMessages), len(expectedMessages))
			fail           = func() {
				done <- &failure{message: fmt.Sprintf("Expected:\n%v\r\nbut received\n%v", expectedMessages, receivedMessages)}
			}
			success = func() {
				done <- &success{}
			}
		)
		copy(remainMessages, expectedMessages)
		for rm := range a.message {
			receivedMessages = append(receivedMessages, rm)

			if len(receivedMessages) > len(expectedMessages) {
				fail()
			}

			found := false
			for idx, em := range remainMessages {
				if (reflect.TypeOf(rm) == reflect.TypeOf(em)) && reflect.DeepEqual(em, rm) {
					remainMessages = append(remainMessages[:idx], remainMessages[idx+1:]...)
					found = true
					break
				}
			}

			if !found {
				fail()
			}
			if len(receivedMessages) == len(expectedMessages) {
				success()
			}
		}
	}()

	select {
	case result := <-done:
		if failure, ok := result.(*failure); ok {
			a.t.Helper()
			a.t.Errorf(failure.message)
		}

		if process, ok := a.system.ProcessRegistry.Get(a.pid); ok {
			process.SendUserMessage(a.pid, result)
		} else {
			panic(fmt.Errorf("No process found for %v", a.pid))
		}
	case <-time.After(a.timeout):
		var msg string
		a.t.Helper()
		mutex.Lock()
		msg = fmt.Sprintf("Timeout. Expected %v but received %v", expectedMessages, receivedMessages)
		mutex.Unlock()
		a.t.Errorf(msg)
		if process, ok := a.system.ProcessRegistry.Get(a.pid); ok {
			process.SendUserMessage(a.pid, &failure{message: msg})
		} else {
			panic(fmt.Errorf("No process found for %v", a.pid))
		}
	}
}

/*
Checks if all expected user defined messages were received with respect of their order
*/
func (a *testProbe) ExpectMessages(msg ...interface{}) {

	var (
		mutex            sync.Mutex
		expectedMessages Expected = msg
		receivedMessages          = make(Expected, 0)
		done                      = make(chan interface{}, 1)
	)

	go func() {

		var (
			remainMessages = make([]interface{}, len(expectedMessages), len(expectedMessages))
			fail           = func() {
				mutex.Lock()
				done <- &failure{message: fmt.Sprintf("Expected:\n%v\nbut received\n%v", expectedMessages, receivedMessages)}
				mutex.Unlock()
			}
			success = func() {
				done <- &success{}
			}
		)

		copy(remainMessages, expectedMessages)

		for rm := range a.message {
			mutex.Lock()
			receivedMessages = append(receivedMessages, rm)
			mutex.Unlock()
			if len(receivedMessages) > len(expectedMessages) {
				fail()
			}

			expected := remainMessages[0]
			if (reflect.TypeOf(expected) != reflect.TypeOf(rm)) || !reflect.DeepEqual(expected, rm) {
				fail()
			}

			remainMessages = remainMessages[1:]

			if len(receivedMessages) == len(expectedMessages) {
				success()
			}
		}
	}()

	select {
	case result := <-done:
		if failure, ok := result.(*failure); ok {
			a.t.Helper()
			a.t.Errorf(failure.message)
		}
		if process, ok := a.system.ProcessRegistry.Get(a.pid); ok {
			process.SendUserMessage(a.pid, result)
		} else {
			panic(fmt.Errorf("No process found for %v", a.pid))
		}
	case <-time.After(a.timeout):

		var msg string
		a.t.Helper()
		mutex.Lock()
		msg = fmt.Sprintf("Timeout. Expected %v but received %v", expectedMessages, receivedMessages)
		mutex.Unlock()
		a.t.Errorf(msg)
		if process, ok := a.system.ProcessRegistry.Get(a.pid); ok {
			process.SendUserMessage(a.pid, &failure{message: msg})
		} else {
			panic(fmt.Errorf("No process found for %v", a.pid))
		}

	}
}

//Terminates the test probe actor
func (a *testProbe) Terminate() {
	a.system.Root.Stop(a.pid)
}
