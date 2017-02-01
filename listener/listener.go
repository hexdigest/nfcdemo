package listener

import (
	"time"

	"github.com/fuzxxl/nfc/2.0/nfc"
	"github.com/hexdigest/nfcdemo"
	"github.com/hexdigest/nfcdemo/emv"
	"github.com/pkg/errors"
)

type logger interface {
	Printf(format string, args ...interface{})
}

type Config struct {
	ConnString      string
	DelayAfterError time.Duration
	TerminalConfig  emv.TerminalConfig
	Logger          logger
}

//Chan connects to the reader using connstring and returns a chan
//that discovered EMV cards will be sent to or an error
func Chan(conf Config) (ch chan emv.Card, error error) {
	device, err := nfc.Open(conf.ConnString)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open connection to reader")
	}

	conf.Logger.Printf("successfully connected to the reader: %s (%s)", device.String(), conf.ConnString)

	if err := device.InitiatorInit(); err != nil {
		return nil, errors.Wrap(err, "failed to init")
	}

	modulation := nfc.Modulation{
		Type:     nfc.ISO14443a,
		BaudRate: nfc.Nbr847,
	}

	sender := nfcdemo.NewAPDUSender(device, conf.Logger)

	ch = make(chan emv.Card)

	go func() {
		defer close(ch)
		for {
			targets, err := device.InitiatorListPassiveTargets(modulation)
			if err != nil {
				conf.Logger.Printf("failed to list passive targets: %v", err)
				time.Sleep(conf.DelayAfterError)
				continue
			}

			for _, t := range targets {
				tt, ok := t.(*nfc.ISO14443aTarget)
				if !ok {
					conf.Logger.Printf("not an ISO14443aTarget: %T\n", t)
					continue
				}

				uid := tt.UID[0:tt.UIDLen]

				if tt.Sak&0x20 == 0 {
					conf.Logger.Printf("target %x does not support ISO14443-4", uid)
					continue
				}

				selectedTarget, err := device.InitiatorSelectPassiveTarget(modulation, uid)
				if err != nil {
					conf.Logger.Printf("failed to select target %x: %v\n", uid, err)
					continue
				}

				conf.Logger.Printf("target successfully selected %T: %x\n", selectedTarget, uid)

				card, err := emv.ProcessTarget(sender, conf.TerminalConfig)
				if err != nil {
					conf.Logger.Printf("failed to process target: %v\n", err)
					device.InitiatorDeselectTarget()
					continue
				}

				conf.Logger.Printf("%s card detected ****%s %d/%d\n", card.Type, card.PAN[len(card.PAN)-4:], card.ExpMonth, card.ExpYear)
				ch <- *card
			}
		}
	}()

	return ch, nil
}
