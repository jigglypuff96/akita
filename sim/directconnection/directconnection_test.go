package directconnection

import (
	"fmt"
	"math/rand"
	"time"

	gomock "github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/sim"
)

var _ = Describe("DirectConnection", func() {

	var (
		mockCtrl   *gomock.Controller
		port1      *MockPort
		port2      *MockPort
		engine     *MockEngine
		connection *Comp
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		port1 = NewMockPort(mockCtrl)
		port2 = NewMockPort(mockCtrl)
		engine = NewMockEngine(mockCtrl)
		connection = MakeBuilder().WithEngine(engine).WithFreq(1).Build("Direct")

		port1.EXPECT().SetConnection(connection)
		connection.PlugIn(port1, 4)

		port2.EXPECT().SetConnection(connection)
		connection.PlugIn(port2, 1)
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("should be panic if msg src is nil", func() {
		msg := sim.NewSampleMsg()
		msg.Src = nil

		Expect(func() { connection.Send(msg) }).To(Panic())
	})

	It("should be panic is src is not connected", func() {
		msg := sim.NewSampleMsg()
		msg.Src = NewMockPort(mockCtrl)
		msg.Dst = NewMockPort(mockCtrl)

		Expect(func() {
			connection.Send(msg)
		}).To(Panic())
	})

	It("should be panic if msg src is the same as dst", func() {
		msg := sim.NewSampleMsg()
		msg.Src = port1
		msg.Dst = port1

		Expect(func() { connection.Send(msg) }).To(Panic())
	})

	It("should buffer the message and schedule tick when a message is sent", func() {
		msg := sim.NewSampleMsg()
		msg.SendTime = 10
		msg.Src = port1
		msg.Dst = port2

		engine.EXPECT().Schedule(gomock.Any()).Do(func(evt sim.TickEvent) {
			Expect(evt.Time()).To(Equal(sim.VTimeInSec(10)))
			Expect(evt.IsSecondary()).To(BeTrue())
		})

		connection.Send(msg)

		Expect(connection.ends[port1].buf).To(ContainElement(msg))
	})

	It("should only tick once for all the messages sent at the same time ", func() {
		msg1 := sim.NewSampleMsg()
		msg1.SendTime = 10
		msg1.Src = port1
		msg1.Dst = port2

		msg2 := sim.NewSampleMsg()
		msg2.SendTime = 10
		msg2.Src = port2
		msg2.Dst = port1

		engine.EXPECT().Schedule(gomock.Any()).Do(func(evt sim.TickEvent) {
			Expect(evt.Time()).To(Equal(sim.VTimeInSec(10)))
			Expect(evt.IsSecondary()).To(BeTrue())
		})

		connection.Send(msg1)
		connection.Send(msg2)

		Expect(connection.ends[port1].buf).To(ContainElement(msg1))
		Expect(connection.ends[port2].buf).To(ContainElement(msg1))
	})

	It("should fail sending if local buffer is full", func() {
		msg1 := sim.NewSampleMsg()
		msg1.ID = "1"
		msg1.SendTime = 10
		msg1.Src = port2
		msg1.Dst = port1

		msg2 := sim.NewSampleMsg()
		msg1.ID = "2"
		msg1.SendTime = 10
		msg2.SendTime = 10
		msg2.Src = port2
		msg2.Dst = port1

		engine.EXPECT().Schedule(gomock.Any()).Do(func(evt sim.TickEvent) {
			Expect(evt.Time()).To(Equal(sim.VTimeInSec(10)))
			Expect(evt.IsSecondary()).To(BeTrue())
		})

		err1 := connection.Send(msg1)
		err2 := connection.Send(msg2)

		Expect(connection.ends[port2].buf).To(ContainElement(msg1))
		Expect(connection.ends[port2].buf).NotTo(ContainElement(msg2))
		Expect(err1).To(BeNil())
		Expect(err2).NotTo(BeNil())
	})

	It("should forward when handling tick event", func() {
		tick := sim.MakeTickEvent(10, connection)

		msg1 := sim.NewSampleMsg()
		msg1.SendTime = 10
		msg1.Src = port1
		msg1.Dst = port2

		msg2 := sim.NewSampleMsg()
		msg2.SendTime = 10
		msg2.Src = port2
		msg2.Dst = port1

		connection.ends[port1].buf = append(connection.ends[port1].buf, msg1)
		connection.ends[port2].buf = append(connection.ends[port2].buf, msg2)
		connection.ends[port2].busy = true

		port1.EXPECT().Recv(msg2).Return(nil)
		port2.EXPECT().Recv(msg1).Return(nil)
		port2.EXPECT().NotifyAvailable(sim.VTimeInSec(10))
		engine.EXPECT().Schedule(gomock.Any()).Do(func(evt sim.TickEvent) {
			Expect(evt.Time()).To(Equal(sim.VTimeInSec(11)))
			Expect(evt.IsSecondary()).To(BeTrue())
		})

		connection.Handle(tick)

		Expect(connection.ends[port1].buf).To(HaveLen(0))
		Expect(connection.ends[port2].buf).To(HaveLen(0))
		Expect(connection.ends[port2].busy).To(BeFalse())
		Expect(msg1.RecvTime).To(Equal(sim.VTimeInSec(10)))
		Expect(msg2.RecvTime).To(Equal(sim.VTimeInSec(10)))
	})
})

type agent struct {
	*sim.TickingComponent

	msgsOut []sim.Msg
	msgsIn  []sim.Msg

	OutPort sim.Port
}

func newAgent(engine sim.Engine, freq sim.Freq, name string) *agent {
	a := new(agent)
	a.TickingComponent = sim.NewTickingComponent(name, engine, freq, a)
	a.OutPort = sim.NewLimitNumMsgPort(a, 4, name+".OutPort")
	return a
}

func (a *agent) Tick(now sim.VTimeInSec) bool {
	madeProgress := false

	msgIn := a.OutPort.Retrieve(now)
	if msgIn != nil {
		a.msgsIn = append(a.msgsIn, msgIn)
		madeProgress = true
	}

	if len(a.msgsOut) > 0 {
		head := a.msgsOut[0]
		head.Meta().SendTime = now
		err := a.OutPort.Send(a.msgsOut[0])
		if err == nil {
			madeProgress = true
			a.msgsOut = a.msgsOut[1:]
		}
	}

	return madeProgress
}

var _ = Describe("Direct Connection Integration", func() {
	var (
		mockCtrl        *gomock.Controller
		engine          sim.Engine
		connection      *Comp
		agents          []*agent
		numAgents       = 10
		numMsgsPerAgent = 1000
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		engine = sim.NewSerialEngine()
		connection = MakeBuilder().WithEngine(engine).WithFreq(1).Build("Conn")
		agents = nil
		for i := 0; i < numAgents; i++ {
			a := newAgent(engine, 1, fmt.Sprintf("Agent[%d]", i))
			agents = append(agents, a)
			connection.PlugIn(a.OutPort, 1)
		}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("should deliver all messages", func() {
		for _, agent := range agents {
			for i := 0; i < numMsgsPerAgent; i++ {
				msg := sim.NewSampleMsg()
				msg.Src = agent.OutPort
				msg.Dst = agents[rand.Intn(len(agents))].OutPort
				for msg.Dst == msg.Src {
					msg.Dst = agents[rand.Intn(len(agents))].OutPort
				}
				msg.ID = fmt.Sprintf("%s(%d)->%s",
					agent.Name(), i, msg.Dst.Component().Name())
				agent.msgsOut = append(agent.msgsOut, msg)
			}
			agent.TickLater(0)
		}

		engine.Run()

		totalRecvedMsgCount := 0
		for _, agent := range agents {
			totalRecvedMsgCount += len(agent.msgsIn)
		}
		Expect(totalRecvedMsgCount).To(Equal(numAgents * numMsgsPerAgent))
	})

	It("should run deterministicly", func() {
		seed := time.Now().UTC().UnixNano()
		time1 := directConnectionTest(seed)
		time2 := directConnectionTest(seed)

		Expect(time1).To(Equal(time2))
	})
})

func directConnectionTest(seed int64) sim.VTimeInSec {
	rand.Seed(seed)
	numAgents := 100
	numMsgsPerAgent := 1000
	engine := sim.NewSerialEngine()
	connection := MakeBuilder().WithEngine(engine).WithFreq(1).Build("Conn")
	agents := make([]*agent, 0, numAgents)

	for i := 0; i < numAgents; i++ {
		a := newAgent(engine, 1, fmt.Sprintf("Agent%d", i))
		agents = append(agents, a)
		connection.PlugIn(a.OutPort, 1)
	}

	for _, agent := range agents {
		for i := 0; i < numMsgsPerAgent; i++ {
			msg := sim.NewSampleMsg()
			msg.Src = agent.OutPort
			msg.Dst = agents[rand.Intn(len(agents))].OutPort
			for msg.Dst == msg.Src {
				msg.Dst = agents[rand.Intn(len(agents))].OutPort
			}
			msg.ID = fmt.Sprintf("%s(%d)->%s",
				agent.Name(), i, msg.Dst.Component().Name())
			agent.msgsOut = append(agent.msgsOut, msg)
		}
		agent.TickLater(0)
	}

	engine.Run()

	return engine.CurrentTime()
}
