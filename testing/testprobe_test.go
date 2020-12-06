package testactor

import (
	"github.com/AsynkronIT/protoactor-go/actor"
	"github.com/stretchr/testify/suite"
	"sync/atomic"
	"testing"
	"time"
)

type ComplexMessage struct {
	name string
}

func TestTestProbe_ExpectUnorderedMessages(t *testing.T) {
	system := actor.NewActorSystem()
	probe := ProbeWithinTest(t, system)

	go func() {
		system.Root.Send(probe.PID(), "first")
		system.Root.Send(probe.PID(), "second")
		system.Root.Send(probe.PID(), 5)
		system.Root.Send(probe.PID(), 3)
		system.Root.Send(probe.PID(), &ComplexMessage{name: "my name"})
	}()

	probe.ExpectUnorderedMessages("first", "second", 3, 5, &ComplexMessage{name: "my name"})
}

func TestTestProbe_ExpectMessages(t *testing.T) {
	system := actor.NewActorSystem()
	probe := ProbeWithinTest(t, system).WithTimeout(time.Second)
	system.Root.Send(probe.PID(), "first")
	system.Root.Send(probe.PID(), "second")
	system.Root.Send(probe.PID(), 3)
	system.Root.Send(probe.PID(), 5)
	system.Root.Send(probe.PID(), &ComplexMessage{name: "my name"})

	probe.ExpectMessages("first", "second", 3, 5, &ComplexMessage{name: "my name"})
}

type TestSuite struct {
	suite.Suite
}

func (s *TestSuite) Test_Suite() {
	s.Run("my internal test", func() {
		system := actor.NewActorSystem()
		probe := ProbeWithinSuite(s, system)
		go func() {
			system.Root.Send(probe.PID(), "first")
			system.Root.Send(probe.PID(), "second")
			system.Root.Send(probe.PID(), 5)
			system.Root.Send(probe.PID(), 3)
			system.Root.Send(probe.PID(), &ComplexMessage{name: "my name"})
		}()

		probe.ExpectUnorderedMessages("first", "second", 3, 5, &ComplexMessage{name: "my name"})
	})
}

func TestSuite_Run(t *testing.T) {
	suite.Run(t, new(TestSuite))
}

func TestTestProbe_AsSenderMiddleware(t *testing.T) {
	system := actor.NewActorSystem()

	probe := ProbeWithinTest(t, system)
	receiver := system.Root.Spawn(actor.PropsFromFunc(func(c actor.Context) {

	}))
	pid := system.Root.Spawn(actor.PropsFromProducer(func() actor.Actor {
		var counter int32 = 0
		var actor actor.ReceiveFunc = func(c actor.Context) {
			switch msg := c.Message().(type) {
			case string:
				c.Send(receiver, "parent: "+msg)
				c.Send(receiver, atomic.AddInt32(&counter, 1))
				c.Send(receiver, atomic.AddInt32(&counter, 1))
			}
		}
		return actor
	}).WithSenderMiddleware(probe.AsSenderMiddleware()))

	system.Root.Send(pid, "my message")
	probe.ExpectMessages("parent: my message", int32(1), int32(2))
	probe.Terminate()
	system.Root.Stop(pid)
	system.Root.Stop(receiver)
}
