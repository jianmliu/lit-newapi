package controller

import (
	"context"
	"errors"
	"math/big"
	"os"
	"strings"
)

// Sub2APIPaymentProvider abstracts the payment surface used by the Sub2API
// marketplace: an inbound CaptureX402 call (for /api/sub2api/buy where the
// buyer settles in Base USDC via x402 + EIP-3009) and an outbound PayoutUSDC
// call (for the admin withdrawal path where source owners are paid out in
// the same Base USDC settlement currency). Real implementations dispatch
// against either an on-chain hot wallet (self-hosted) or a third-party
// facilitator (Stripe, Creem, Circle Wallets); the env-driven factory
// below returns a "not configured" provider by default so an operator who
// has not opted into a payment backend cannot accidentally approve money
// movements.
type Sub2APIPaymentProvider interface {
	CaptureX402(ctx context.Context, paymentHeader string, expectedAtoms *big.Int) (txHash string, err error)
	PayoutUSDC(ctx context.Context, to string, amount *big.Int) (txHash string, err error)
	Close()
}

type Sub2APIPaymentProviderFactory func(ctx context.Context) (Sub2APIPaymentProvider, error)

var sub2APINewPaymentProvider Sub2APIPaymentProviderFactory = newSub2APIPaymentProviderFromEnv

// SetSub2APIPaymentProviderForTest swaps the package-level factory and
// returns a restore function. Only intended for tests that need to inject
// a fake provider; production code goes through the env-driven factory.
func SetSub2APIPaymentProviderForTest(factory Sub2APIPaymentProviderFactory) func() {
	previous := sub2APINewPaymentProvider
	sub2APINewPaymentProvider = factory
	return func() { sub2APINewPaymentProvider = previous }
}

func sub2APIPaymentProviderName() string {
	name := strings.ToLower(strings.TrimSpace(os.Getenv("SUB2API_PAYMENT_PROVIDER")))
	if name == "" {
		return "not_configured"
	}
	return name
}

func newSub2APIPaymentProviderFromEnv(ctx context.Context) (Sub2APIPaymentProvider, error) {
	_ = ctx
	switch sub2APIPaymentProviderName() {
	case "self_hosted":
		return nil, errors.New("Sub2API payment provider 'self_hosted' x402 capture is not yet implemented in lit-newapi; port from lit-substrate/shared/x402auth")
	case "x402_facilitator":
		return nil, errors.New("Sub2API payment provider 'x402_facilitator' is not yet implemented")
	case "circle_wallets":
		return nil, errors.New("Sub2API payment provider 'circle_wallets' is not yet implemented")
	case "not_configured":
		return nil, errors.New("Sub2API payment provider is not configured (set SUB2API_PAYMENT_PROVIDER)")
	default:
		return nil, errors.New("Sub2API payment provider mode is unknown: " + sub2APIPaymentProviderName())
	}
}
