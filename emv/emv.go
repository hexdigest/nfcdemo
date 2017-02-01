package emv

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"io"
	"math/rand"

	"github.com/hexdigest/apdu"
	"github.com/hexdigest/bertlv"
	"github.com/pkg/errors"
)

const ( //Some useful EMV tags
	TagApplicationID                           = 0x4F
	TagApplicationLabel                        = 0x50
	TagProcessingOptionsDataObjectList         = 0x9f38
	TagApplicationFileLocator                  = 0x94
	TagCardholderName                          = 0x5f20
	TagApplicationExpirationDate               = 0x5F24
	TagApplicationPrimaryAccountNumber         = 0x5a
	TagApplicationPrimaryAccountSequenceNumber = 0x5f34 //sequence number among the cards with the same PAN
	TagApplicationCurrencyCode                 = 0x9f42
	TagLanguagePreference                      = 0x5f2d

	TagTerminalTransactionQualifiers = 0x9f66
	TagAmountAuthorized              = 0x9f02
	TagUnpredictableNumber           = 0x9f37
	TagTransactionCurrencyCode       = 0x5f2a
	TagTerminalCountryCode           = 0x9f1a
	TagCommandTemplate               = 0x83
)

type Card struct {
	Type     string
	PAN      string
	ExpYear  uint8
	ExpMonth uint8
}

type TerminalConfig struct {
	TerminalTransactionQualifiers uint32
	TransactionCurrencyCode       uint16
	TerminalCountryCode           uint16
}

type apduSender interface {
	Send(apdu.APDU) ([]byte, error)
}

var apduGetApplicationList = apdu.Select([]byte{0x32, 0x50, 0x41, 0x59, 0x2E, 0x53, 0x59, 0x53, 0x2E, 0x44, 0x44, 0x46, 0x30, 0x31})

func ProcessTarget(sender apduSender, termConfig TerminalConfig) (*Card, error) {
	rx, err := sender.Send(apduGetApplicationList)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get application list")
	}

	aid, err := bertlv.Find(TagApplicationID, rx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to find AID")
	}

	rx, err = sender.Send(apdu.Select(aid.V))
	if err != nil {
		return nil, errors.Wrap(err, "failed to select AID")
	}

	label, err := bertlv.Find(TagApplicationLabel, rx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to find application label")
	}

	pdol, err := bertlv.Find(TagProcessingOptionsDataObjectList, rx)
	if err != nil && err != bertlv.ErrNotFound {
		return nil, errors.Wrap(err, "failed to find PDOL")
	}

	apduGPO, err := CreateGPOCommand(pdol, termConfig)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create GPO command")
	}

	rx, err = sender.Send(*apduGPO)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get processing options")
	}

	afl, err := bertlv.Find(TagApplicationFileLocator, rx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to find AFL")
	}

	if len(afl.V)%4 != 0 {
		return nil, errors.Errorf("invalid AFL length")
	}

	card, err := ReadRecords(sender, afl.V)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read records")
	}
	card.Type = string(label.V)

	return card, nil
}

var errNoPAN = errors.New("PAN not found")

func ReadRecords(sender apduSender, afl []byte) (*Card, error) {
	var apduReadRecord = apdu.APDU{Ins: 0xb2}

	for i := 0; i < len(afl)/4; i++ {
		chunk := afl[i*4 : (i+1)*4]
		sfi := (chunk[0] & 0xF8) | 0x04
		firstRec := chunk[1]
		lastRec := chunk[2]

		for rec := firstRec; rec <= lastRec; rec++ {
			apduReadRecord.P1 = rec
			apduReadRecord.P2 = sfi

			rx, err := sender.Send(apduReadRecord)
			if err != nil {
				return nil, errors.Wrap(err, "failed to send ReadRecord command")
			}

			card, err := ParseRecord(rx)
			if err == bertlv.ErrNotFound {
				continue
			}

			if err != nil {
				return nil, errors.Wrap(err, "failed to parse record")
			}

			return card, nil
		}
	}

	return nil, errNoPAN
}

func ParseRecord(record []byte) (*Card, error) {
	var card Card

	pan, err := bertlv.Find(TagApplicationPrimaryAccountNumber, record)
	if err != nil {
		return nil, err
	}

	card.PAN = hex.EncodeToString(pan.V)

	expDate, err := bertlv.Find(TagApplicationExpirationDate, record)
	if err != nil {
		return nil, errors.Wrap(err, "failed to find expiration date")
	}

	card.ExpYear = (expDate.V[0] >> 4 * 10) + (expDate.V[0] & 0xf)  //Decoding BCD encoded expiration year
	card.ExpMonth = (expDate.V[1] >> 4 * 10) + (expDate.V[1] & 0xf) //Decoding BCD encoded expiration month

	return &card, nil
}

// CreateGPOCommand creates GPO APDU command using PDOL and terminal config
func CreateGPOCommand(pdol *bertlv.TagValue, termConfig TerminalConfig) (*apdu.APDU, error) {
	a := apdu.APDU{Cla: 0x80, Ins: 0xa8}
	if pdol == nil {
		a.Data = []byte{0x83, 0x00}
		return &a, nil
	}

	r := bytes.NewReader(pdol.V)

	var data []byte

	for {
		tag, n, err := bertlv.ReadTag(r)
		if err == io.EOF || n == 0 {
			break
		}

		if err != nil {
			return nil, err
		}

		l, _, err := bertlv.ReadLen(r)
		if err != nil {
			return nil, err
		}

		d := make([]byte, l)

		switch tag {
		case TagTerminalTransactionQualifiers:
			binary.BigEndian.PutUint32(d, termConfig.TerminalTransactionQualifiers)
		case TagAmountAuthorized:
			encodeBCD(d, uint64(1000))
		case TagUnpredictableNumber:
			encodeBCD(d, uint64(rand.Uint32()))
		case TagTransactionCurrencyCode:
			encodeBCD(d, uint64(termConfig.TransactionCurrencyCode))
		case TagTerminalCountryCode:
			encodeBCD(d, uint64(termConfig.TerminalCountryCode))
		}

		data = append(data, d...)
	}

	a.Data = bertlv.TagValue{T: TagCommandTemplate, V: data}.Bytes()
	return &a, nil
}

func encodeBCD(p []byte, u uint64) {
	if u == 0 {
		return
	}

	rem := u
	for i := len(p) - 1; i >= 0 && rem > 0; i-- {
		tail := byte(rem % 100)
		hi := tail / 10
		lo := tail % 10
		p[i] = hi<<4 + lo
		rem = rem / 100
	}
}
