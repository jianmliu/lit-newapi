package controller

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"

	x402 "github.com/QuantumNous/new-api/controller/x402auth"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
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

var sub2APIX402Nonces = x402.NewNonceStore()

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
	switch sub2APIPaymentProviderName() {
	case "self_hosted":
		return newSelfHostedSub2APIPaymentProvider(ctx)
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

type selfHostedSub2APIPaymentProvider struct {
	client     x402.EVMClient
	close      func()
	privateKey *ecdsa.PrivateKey
	usdc       ethcommon.Address
	hotWallet  ethcommon.Address
	domain     x402.AuthorizationDomain
	nonces     *x402.NonceStore
}

func newSelfHostedSub2APIPaymentProvider(ctx context.Context) (*selfHostedSub2APIPaymentProvider, error) {
	rpcURL := strings.TrimSpace(os.Getenv("SUB2API_EVM_RPC_URL"))
	if rpcURL == "" {
		return nil, errors.New("SUB2API_EVM_RPC_URL is not configured")
	}
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("SUB2API_EVM_RPC_URL is unavailable: %w", err)
	}
	provider, err := newSelfHostedSub2APIPaymentProviderWithClient(ctx, client, client.Close, sub2APIX402Nonces)
	if err != nil {
		client.Close()
		return nil, err
	}
	return provider, nil
}

func newSelfHostedSub2APIPaymentProviderWithClient(ctx context.Context, client x402.EVMClient, closeFn func(), nonces *x402.NonceStore) (*selfHostedSub2APIPaymentProvider, error) {
	privateKeyRaw := strings.TrimSpace(os.Getenv("SUB2API_HOT_WALLET_PRIVATE_KEY"))
	if privateKeyRaw == "" {
		return nil, errors.New("SUB2API_HOT_WALLET_PRIVATE_KEY is not configured")
	}
	privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyRaw, "0x"))
	if err != nil {
		return nil, errors.New("SUB2API_HOT_WALLET_PRIVATE_KEY is invalid")
	}
	usdcRaw := strings.TrimSpace(os.Getenv("SUB2API_USDC_ADDRESS"))
	if !ethcommon.IsHexAddress(usdcRaw) {
		return nil, errors.New("SUB2API_USDC_ADDRESS is invalid or not configured")
	}
	chainIDRaw := strings.TrimSpace(os.Getenv("SUB2API_CHAIN_ID"))
	if chainIDRaw == "" {
		return nil, errors.New("SUB2API_CHAIN_ID is not configured")
	}
	chainIDInt, err := strconv.ParseInt(chainIDRaw, 10, 64)
	if err != nil || chainIDInt <= 0 {
		return nil, errors.New("SUB2API_CHAIN_ID is invalid")
	}
	configuredChainID := big.NewInt(chainIDInt)
	if client != nil {
		actualChainID, err := client.ChainID(ctx)
		if err != nil {
			return nil, fmt.Errorf("SUB2API_EVM_RPC_URL chain id unavailable: %w", err)
		}
		if actualChainID.Cmp(configuredChainID) != 0 {
			return nil, fmt.Errorf("SUB2API_CHAIN_ID mismatch: configured %s, rpc %s", configuredChainID.String(), actualChainID.String())
		}
	}
	usdc := ethcommon.HexToAddress(usdcRaw)
	return &selfHostedSub2APIPaymentProvider{
		client:     client,
		close:      closeFn,
		privateKey: privateKey,
		usdc:       usdc,
		hotWallet:  crypto.PubkeyToAddress(privateKey.PublicKey),
		domain: x402.AuthorizationDomain{
			Name:              sub2APIUSDCAuthDomainName(configuredChainID),
			Version:           "2",
			ChainID:           configuredChainID,
			VerifyingContract: usdc,
		},
		nonces: nonces,
	}, nil
}

func sub2APIUSDCAuthDomainName(chainID *big.Int) string {
	if chainID != nil && chainID.Cmp(big.NewInt(84532)) == 0 {
		return "USDC"
	}
	return "USD Coin"
}

func (p *selfHostedSub2APIPaymentProvider) CaptureX402(ctx context.Context, paymentHeader string, expectedAtoms *big.Int) (string, error) {
	if p == nil || p.client == nil || p.privateKey == nil || p.usdc == (ethcommon.Address{}) || p.hotWallet == (ethcommon.Address{}) {
		return "", errors.New("Sub2API self_hosted payment provider is not configured")
	}
	if expectedAtoms == nil || expectedAtoms.Sign() <= 0 {
		return "", errors.New("Sub2API x402 expected amount is invalid")
	}
	captured, err := x402.CapturePayment(ctx, x402.CapturePaymentInput{
		Domain:         p.domain,
		Nonces:         p.nonces,
		HotWallet:      p.hotWallet,
		ExpectedAmount: expectedAtoms,
		PaymentHeader:  paymentHeader,
		Capturer:       x402.NewEIP3009Capturer(p.client, p.privateKey, p.usdc),
	})
	if err != nil {
		return "", err
	}
	return captured.TxHash.Hex(), nil
}

func (p *selfHostedSub2APIPaymentProvider) PayoutUSDC(ctx context.Context, to string, amount *big.Int) (string, error) {
	if p == nil || p.client == nil || p.privateKey == nil || p.usdc == (ethcommon.Address{}) {
		return "", errors.New("Sub2API self_hosted payment provider is not configured")
	}
	to = strings.TrimSpace(to)
	if !ethcommon.IsHexAddress(to) {
		return "", errors.New("invalid Base USDC payout account")
	}
	if amount == nil || amount.Sign() <= 0 {
		return "", errors.New("invalid Base USDC payout amount")
	}
	txHash, err := x402.NewERC20Transferer(p.client, p.privateKey, p.usdc).Transfer(ctx, ethcommon.HexToAddress(to), amount)
	if err != nil {
		return "", err
	}
	return txHash.Hex(), nil
}

func (p *selfHostedSub2APIPaymentProvider) Close() {
	if p != nil && p.close != nil {
		p.close()
	}
}
