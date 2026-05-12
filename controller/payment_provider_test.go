package controller

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	x402 "github.com/QuantumNous/new-api/controller/x402auth"
	"github.com/ethereum/go-ethereum"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type fakeSub2APIEVMClient struct {
	chainID    *big.Int
	nonce      uint64
	gas        uint64
	tx         *types.Transaction
	msg        ethereum.CallMsg
	sendCalls  int
	receiptErr error
}

func (f *fakeSub2APIEVMClient) PendingNonceAt(context.Context, ethcommon.Address) (uint64, error) {
	return f.nonce, nil
}

func (f *fakeSub2APIEVMClient) SuggestGasPrice(context.Context) (*big.Int, error) {
	return big.NewInt(1_000_000_000), nil
}

func (f *fakeSub2APIEVMClient) EstimateGas(_ context.Context, msg ethereum.CallMsg) (uint64, error) {
	f.msg = msg
	return f.gas, nil
}

func (f *fakeSub2APIEVMClient) ChainID(context.Context) (*big.Int, error) { return f.chainID, nil }

func (f *fakeSub2APIEVMClient) SendTransaction(_ context.Context, tx *types.Transaction) error {
	f.sendCalls++
	f.tx = tx
	return nil
}

func (f *fakeSub2APIEVMClient) TransactionReceipt(context.Context, ethcommon.Hash) (*types.Receipt, error) {
	if f.receiptErr != nil {
		return nil, f.receiptErr
	}
	return &types.Receipt{Status: types.ReceiptStatusSuccessful}, nil
}

func TestSelfHostedPaymentProviderCaptureVerifiesRealSignatureAndRejectsReplay(t *testing.T) {
	hotKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("Generate hot key: %v", err)
	}
	buyerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("Generate buyer key: %v", err)
	}
	chainID := big.NewInt(8453)
	usdc := ethcommon.HexToAddress("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913")
	t.Setenv("SUB2API_HOT_WALLET_PRIVATE_KEY", hex.EncodeToString(crypto.FromECDSA(hotKey)))
	t.Setenv("SUB2API_USDC_ADDRESS", usdc.Hex())
	t.Setenv("SUB2API_CHAIN_ID", chainID.String())
	client := &fakeSub2APIEVMClient{chainID: chainID, nonce: 9, gas: 80_000}
	provider, err := newSelfHostedSub2APIPaymentProviderWithClient(context.Background(), client, nil, x402.NewNonceStore())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	now := time.Now().UTC()
	auth := x402.TransferWithAuthorization{
		From:        crypto.PubkeyToAddress(buyerKey.PublicKey),
		To:          provider.hotWallet,
		Value:       big.NewInt(2_000_000),
		ValidAfter:  uint64(now.Add(-time.Minute).Unix()),
		ValidBefore: uint64(now.Add(time.Minute).Unix()),
		Nonce:       testSub2APINonce(0x42),
	}
	header := makeSub2APIPaymentHeader(t, provider.domain, auth, buyerKey, "req_capture")
	txHash, err := provider.CaptureX402(context.Background(), header, big.NewInt(2_000_000))
	if err != nil {
		t.Fatalf("CaptureX402: %v", err)
	}
	if client.tx == nil || txHash != client.tx.Hash().Hex() {
		t.Fatalf("tx hash = %s sent tx = %v", txHash, client.tx)
	}
	if client.msg.To == nil || *client.msg.To != usdc {
		t.Fatalf("estimate to = %v, want USDC", client.msg.To)
	}
	if gotSelector := ethcommon.Bytes2Hex(client.tx.Data()[:4]); gotSelector != "e3ee160e" {
		t.Fatalf("selector = %s, want transferWithAuthorization", gotSelector)
	}
	if _, err := provider.CaptureX402(context.Background(), header, big.NewInt(2_000_000)); err == nil || (!errors.Is(err, x402.ErrAuthorizationReplay) && !strings.Contains(err.Error(), x402.ErrAuthorizationReplay.Error())) {
		t.Fatalf("replay err = %v, want ErrAuthorizationReplay", err)
	}
	if client.sendCalls != 1 {
		t.Fatalf("send calls = %d, want replay rejected before second submit", client.sendCalls)
	}
}

func TestSelfHostedPaymentProviderCaptureRejectsWrongAmountBeforeSubmit(t *testing.T) {
	hotKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("Generate hot key: %v", err)
	}
	buyerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("Generate buyer key: %v", err)
	}
	chainID := big.NewInt(8453)
	usdc := ethcommon.HexToAddress("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913")
	t.Setenv("SUB2API_HOT_WALLET_PRIVATE_KEY", hex.EncodeToString(crypto.FromECDSA(hotKey)))
	t.Setenv("SUB2API_USDC_ADDRESS", usdc.Hex())
	t.Setenv("SUB2API_CHAIN_ID", chainID.String())
	client := &fakeSub2APIEVMClient{chainID: chainID, nonce: 9, gas: 80_000}
	provider, err := newSelfHostedSub2APIPaymentProviderWithClient(context.Background(), client, nil, x402.NewNonceStore())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	now := time.Now().UTC()
	auth := x402.TransferWithAuthorization{
		From:        crypto.PubkeyToAddress(buyerKey.PublicKey),
		To:          provider.hotWallet,
		Value:       big.NewInt(2_000_001),
		ValidAfter:  uint64(now.Add(-time.Minute).Unix()),
		ValidBefore: uint64(now.Add(time.Minute).Unix()),
		Nonce:       testSub2APINonce(0x43),
	}
	header := makeSub2APIPaymentHeader(t, provider.domain, auth, buyerKey, "req_wrong_amount")
	_, err = provider.CaptureX402(context.Background(), header, big.NewInt(2_000_000))
	if err == nil || (!errors.Is(err, x402.ErrAuthorizationAmount) && !strings.Contains(err.Error(), x402.ErrAuthorizationAmount.Error())) {
		t.Fatalf("amount err = %v, want ErrAuthorizationAmount", err)
	}
	if client.sendCalls != 0 {
		t.Fatalf("send calls = %d, want no submit", client.sendCalls)
	}
}

func TestSelfHostedPaymentProviderCaptureReturnsTxHashAfterReceiptRPCError(t *testing.T) {
	hotKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("Generate hot key: %v", err)
	}
	buyerKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("Generate buyer key: %v", err)
	}
	chainID := big.NewInt(8453)
	usdc := ethcommon.HexToAddress("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913")
	t.Setenv("SUB2API_HOT_WALLET_PRIVATE_KEY", hex.EncodeToString(crypto.FromECDSA(hotKey)))
	t.Setenv("SUB2API_USDC_ADDRESS", usdc.Hex())
	t.Setenv("SUB2API_CHAIN_ID", chainID.String())
	client := &fakeSub2APIEVMClient{chainID: chainID, nonce: 9, gas: 80_000, receiptErr: errors.New("receipt rpc unavailable after broadcast")}
	provider, err := newSelfHostedSub2APIPaymentProviderWithClient(context.Background(), client, nil, x402.NewNonceStore())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	now := time.Now().UTC()
	auth := x402.TransferWithAuthorization{
		From:        crypto.PubkeyToAddress(buyerKey.PublicKey),
		To:          provider.hotWallet,
		Value:       big.NewInt(2_000_000),
		ValidAfter:  uint64(now.Add(-time.Minute).Unix()),
		ValidBefore: uint64(now.Add(time.Minute).Unix()),
		Nonce:       testSub2APINonce(0x44),
	}
	header := makeSub2APIPaymentHeader(t, provider.domain, auth, buyerKey, "req_receipt_rpc_error")
	txHash, err := provider.CaptureX402(context.Background(), header, big.NewInt(2_000_000))
	if err != nil {
		t.Fatalf("CaptureX402 returned post-broadcast receipt error: %v", err)
	}
	if client.tx == nil || txHash != client.tx.Hash().Hex() {
		t.Fatalf("tx hash = %s sent tx = %v", txHash, client.tx)
	}
	if client.sendCalls != 1 {
		t.Fatalf("send calls = %d, want 1", client.sendCalls)
	}
}

func TestSelfHostedPaymentProviderPayoutReturnsTxHashAfterReceiptRPCError(t *testing.T) {
	hotKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("Generate hot key: %v", err)
	}
	chainID := big.NewInt(8453)
	usdc := ethcommon.HexToAddress("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913")
	t.Setenv("SUB2API_HOT_WALLET_PRIVATE_KEY", hex.EncodeToString(crypto.FromECDSA(hotKey)))
	t.Setenv("SUB2API_USDC_ADDRESS", usdc.Hex())
	t.Setenv("SUB2API_CHAIN_ID", chainID.String())
	client := &fakeSub2APIEVMClient{chainID: chainID, nonce: 9, gas: 80_000, receiptErr: errors.New("receipt rpc unavailable after broadcast")}
	provider, err := newSelfHostedSub2APIPaymentProviderWithClient(context.Background(), client, nil, x402.NewNonceStore())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	txHash, err := provider.PayoutUSDC(context.Background(), "0x1000000000000000000000000000000000000001", big.NewInt(1_000_000))
	if err != nil {
		t.Fatalf("PayoutUSDC returned post-broadcast receipt error: %v", err)
	}
	if client.tx == nil || txHash != client.tx.Hash().Hex() {
		t.Fatalf("tx hash = %s sent tx = %v", txHash, client.tx)
	}
	if gotSelector := ethcommon.Bytes2Hex(client.tx.Data()[:4]); gotSelector != "a9059cbb" {
		t.Fatalf("selector = %s, want transfer", gotSelector)
	}
	if client.sendCalls != 1 {
		t.Fatalf("send calls = %d, want 1", client.sendCalls)
	}
}

func makeSub2APIPaymentHeader(t *testing.T, domain x402.AuthorizationDomain, auth x402.TransferWithAuthorization, buyerKey *ecdsa.PrivateKey, requestID string) string {
	t.Helper()
	digest, err := domain.Digest(&auth)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	sig, err := crypto.Sign(digest[:], buyerKey)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	payload := map[string]any{
		"version":    x402.Version,
		"scheme":     "eip-3009",
		"request_id": requestID,
		"authorization": map[string]any{
			"from":         auth.From.Hex(),
			"to":           auth.To.Hex(),
			"value":        auth.Value.String(),
			"valid_after":  auth.ValidAfter,
			"valid_before": auth.ValidBefore,
			"nonce":        "0x" + hex.EncodeToString(auth.Nonce[:]),
		},
		"signature_hex": "0x" + hex.EncodeToString(sig),
	}
	out, err := common.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal payment header: %v", err)
	}
	return string(out)
}

func testSub2APINonce(b byte) [32]byte {
	var nonce [32]byte
	for i := range nonce {
		nonce[i] = b
	}
	return nonce
}
