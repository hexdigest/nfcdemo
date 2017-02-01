package nfcdemo

import (
	"github.com/hexdigest/apdu"
)

type transciever interface {
	InitiatorTransceiveBytes(tx, rx []byte, timeoutMs int) (int, error)
}

type logger interface {
	Printf(format string, args ...interface{})
}

type APDUSender struct {
	tr  transciever
	log logger
	rx  []byte
}

func NewAPDUSender(tr transciever, lg logger) APDUSender {
	return APDUSender{tr: tr, log: lg, rx: make([]byte, 65535)}
}

func (s APDUSender) Send(a apdu.APDU) ([]byte, error) {
	b := a.Bytes()
	s.log.Printf("-> %x\n", b)

	n, err := s.tr.InitiatorTransceiveBytes(b, s.rx, 0)
	if err != nil {
		return nil, err
	}

	s.log.Printf("<- %x\n", s.rx[:n])

	return apdu.ParseResponse(s.rx[:n])
}
