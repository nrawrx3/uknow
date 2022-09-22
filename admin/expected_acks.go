package admin

import (
	"log"
	"strings"
	"sync"
	"time"
)

// A struct containing info about acks being waited on by server. Note that
// ackId, and ackerPlayerName, must completely specify the expectedAck entity.
type expectedAck struct {
	ackId           string
	ackerPlayerName string
}

type pendingAck struct {
	expectedAck

	enqueueTime     time.Time
	timeout         time.Duration
	ackReceivedChan chan struct{}
	onAck           func()
	onTimeout       func()
}

func (ack expectedAck) equal(ack1 expectedAck) bool {
	return ack.ackId == ack1.ackId && ack.ackerPlayerName == ack1.ackerPlayerName
}

type expectedAcksList struct {
	mu               sync.Mutex
	pendingAcks      []*pendingAck
	chNewAckReceived chan expectedAck
	logger           *log.Logger
}

func newExpectedAcksState(logger *log.Logger) *expectedAcksList {
	return &expectedAcksList{
		pendingAcks:      make([]*pendingAck, 0, 16),
		chNewAckReceived: make(chan expectedAck),
		logger:           logger,
	}
}

func (es *expectedAcksList) addPending(ack expectedAck, timeout time.Duration, onAck, onTimeout func()) {
	es.mu.Lock()
	es.logger.Printf("Adding new expecting ack to list %+v", ack)

	pendingAck := &pendingAck{
		expectedAck:     ack,
		enqueueTime:     time.Now(),
		timeout:         timeout,
		onAck:           onAck,
		onTimeout:       onTimeout,
		ackReceivedChan: make(chan struct{}),
	}

	go func() {
		timer := time.NewTimer(pendingAck.timeout)
		select {
		case <-timer.C:
			onTimeout()
		case <-pendingAck.ackReceivedChan:
			timer.Stop()
			onAck()
		}
	}()

	es.pendingAcks = append(es.pendingAcks, pendingAck)
	es.mu.Unlock()
}

func (es *expectedAcksList) ackIds() string {
	es.mu.Lock()
	defer es.mu.Unlock()
	var sb strings.Builder
	for _, ack := range es.pendingAcks {
		sb.WriteString(ack.ackId)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (es *expectedAcksList) waitForAcks() {
	for expectedAck := range es.chNewAckReceived {
		es.mu.Lock()

		for i, pendingAck := range es.pendingAcks {
			if !pendingAck.expectedAck.equal(expectedAck) {
				continue
			}

			es.logger.Printf("Acking the ack: %s", pendingAck.ackId)

			pendingAck.ackReceivedChan <- struct{}{}
			es.pendingAcks = append(es.pendingAcks[0:i], es.pendingAcks[i+1:len(es.pendingAcks)]...)
			break
		}

		es.mu.Unlock()
	}
}
