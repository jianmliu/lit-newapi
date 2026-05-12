package x402auth

import (
	"encoding/hex"
	"io"
	"math/big"
	"strings"
	"time"

	appcommon "github.com/QuantumNous/new-api/common"
	ethcommon "github.com/ethereum/go-ethereum/common"
)

const Version = "x402-1"

type VerifyPaymentHeaderInput struct {
	Domain         AuthorizationDomain
	Nonces         *NonceStore
	HotWallet      ethcommon.Address
	ExpectedAmount *big.Int
	Now            time.Time
	PaymentHeader  string
}

func VerifyPaymentHeader(in VerifyPaymentHeaderInput) (ethcommon.Address, error) {
	payment, err := DecodePaymentHeader(in.PaymentHeader)
	if err != nil {
		return ethcommon.Address{}, err
	}
	auth, err := payment.Authorization.toAuthorization()
	if err != nil {
		return ethcommon.Address{}, err
	}
	if err := rejectReplay(in.Nonces, auth.From, auth.Nonce, in.Now); err != nil {
		return ethcommon.Address{}, err
	}
	return VerifyTransferWithAuthorization(in.Domain, auth, payment.Signature, VerifyOptions{ExpectedFrom: auth.From, ExpectedTo: in.HotWallet, ExpectedValue: in.ExpectedAmount, Now: in.Now})
}

type PaymentHeader struct {
	Version       string                    `json:"version"`
	Scheme        string                    `json:"scheme"`
	RequestID     string                    `json:"request_id"`
	Authorization transferAuthorizationJSON `json:"authorization"`
	SignatureHex  string                    `json:"signature_hex"`
	Signature     []byte                    `json:"-"`
}

type transferAuthorizationJSON struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  uint64 `json:"valid_after"`
	ValidBefore uint64 `json:"valid_before"`
	Nonce       string `json:"nonce"`
}

func DecodePaymentHeader(raw string) (*PaymentHeader, error) {
	if raw == "" {
		return nil, io.EOF
	}
	var header PaymentHeader
	if err := appcommon.Unmarshal([]byte(raw), &header); err != nil {
		return nil, err
	}
	if header.Version != Version || header.Scheme != "eip-3009" || header.RequestID == "" {
		return nil, io.ErrUnexpectedEOF
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(header.SignatureHex, "0x"))
	if err != nil || len(sig) != signatureLen {
		return nil, io.ErrUnexpectedEOF
	}
	header.Signature = sig
	return &header, nil
}

func (j transferAuthorizationJSON) toAuthorization() (TransferWithAuthorization, error) {
	if !ethcommon.IsHexAddress(j.From) || !ethcommon.IsHexAddress(j.To) {
		return TransferWithAuthorization{}, io.ErrUnexpectedEOF
	}
	value, ok := new(big.Int).SetString(j.Value, 10)
	if !ok {
		return TransferWithAuthorization{}, io.ErrUnexpectedEOF
	}
	nonceBytes, err := hex.DecodeString(strings.TrimPrefix(j.Nonce, "0x"))
	if err != nil || len(nonceBytes) != 32 {
		return TransferWithAuthorization{}, io.ErrUnexpectedEOF
	}
	var nonce [32]byte
	copy(nonce[:], nonceBytes)
	return TransferWithAuthorization{From: ethcommon.HexToAddress(j.From), To: ethcommon.HexToAddress(j.To), Value: value, ValidAfter: j.ValidAfter, ValidBefore: j.ValidBefore, Nonce: nonce}, nil
}

func rejectReplay(nonces *NonceStore, from ethcommon.Address, nonce [32]byte, now time.Time) error {
	if nonces == nil {
		return nil
	}
	nonces.mu.Lock()
	defer nonces.mu.Unlock()
	byNonce := nonces.used[from]
	if byNonce == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	for n, usedAt := range byNonce {
		if !usedAt.After(now.Add(-nonceReplayWindow)) {
			delete(byNonce, n)
		}
	}
	if usedAt, exists := byNonce[nonce]; exists && usedAt.After(now.Add(-nonceReplayWindow)) {
		return ErrAuthorizationReplay
	}
	return nil
}
