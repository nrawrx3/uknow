package client

import (
	"log"

	"github.com/nrawrx3/uknow"
)

// FILTHY(@rk):TODO(@rk): We will delete this when we implement decision event consuming in the ui itself.
// var DummyCardTransferEventConsumerChan chan uknow.CardTransferEvent

// FILTHY(@rk):TODO(@rk): See DummyCardTransferEventConsumerChan
type dummyCardTransferEventConsumer struct {
	decisionEventPullChan <-chan uknow.CardTransferEvent
}

func (d *dummyCardTransferEventConsumer) RunConsumer(logger *log.Logger, localPlayerName string) {
	for event := range d.decisionEventPullChan {
		logger.Printf("DummTransferConsumer: Transfer event received: %s", event.String(localPlayerName))
	}
}
