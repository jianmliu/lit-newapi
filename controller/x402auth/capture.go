package x402auth

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"
)

type Capturer interface {
	Submit(ctx context.Context, auth TransferWithAuthorization, sig []byte) (ethcommon.Hash, error)
}

type CapturedPayment struct {
	Buyer  ethcommon.Address
	Amount *big.Int
	Nonce  [32]byte
	TxHash ethcommon.Hash
}

type CapturePaymentInput struct {
	Domain         AuthorizationDomain
	Nonces         *NonceStore
	HotWallet      ethcommon.Address
	ExpectedAmount *big.Int
	Now            time.Time
	PaymentHeader  string
	Capturer       Capturer
}

func CapturePayment(ctx context.Context, in CapturePaymentInput) (*CapturedPayment, error) {
	if in.Capturer == nil {
		return nil, errors.New("x402.CapturePayment: nil capturer")
	}
	payment, err := DecodePaymentHeader(in.PaymentHeader)
	if err != nil {
		return nil, fmt.Errorf("x402.CapturePayment: decode payment: %w", err)
	}
	auth, err := payment.Authorization.toAuthorization()
	if err != nil {
		return nil, fmt.Errorf("x402.CapturePayment: authorization: %w", err)
	}
	if err := rejectReplay(in.Nonces, auth.From, auth.Nonce, in.Now); err != nil {
		return nil, fmt.Errorf("x402.CapturePayment: verify: %w", err)
	}
	if _, err := VerifyTransferWithAuthorization(in.Domain, auth, payment.Signature, VerifyOptions{ExpectedFrom: auth.From, ExpectedTo: in.HotWallet, ExpectedValue: in.ExpectedAmount, Now: in.Now}); err != nil {
		return nil, fmt.Errorf("x402.CapturePayment: verify: %w", err)
	}
	txHash, err := in.Capturer.Submit(ctx, auth, payment.Signature)
	if err != nil {
		return nil, fmt.Errorf("x402.CapturePayment: submit: %w", err)
	}
	if in.Nonces != nil {
		if err := in.Nonces.MarkUsedAt(auth.From, auth.Nonce, effectiveNow(in.Now)); err != nil {
			return nil, fmt.Errorf("x402.CapturePayment: nonce: %w", err)
		}
	}
	return &CapturedPayment{Buyer: auth.From, Amount: auth.Value, Nonce: auth.Nonce, TxHash: txHash}, nil
}

func effectiveNow(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now()
	}
	return now
}
