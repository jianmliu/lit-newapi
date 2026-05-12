package x402auth

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type EVMClient interface {
	PendingNonceAt(context.Context, ethcommon.Address) (uint64, error)
	SuggestGasPrice(context.Context) (*big.Int, error)
	EstimateGas(context.Context, ethereum.CallMsg) (uint64, error)
	ChainID(context.Context) (*big.Int, error)
	SendTransaction(context.Context, *types.Transaction) error
	TransactionReceipt(context.Context, ethcommon.Hash) (*types.Receipt, error)
}

type EIP3009Capturer struct {
	client  EVMClient
	key     *ecdsa.PrivateKey
	from    ethcommon.Address
	usdc    ethcommon.Address
	poll    time.Duration
	timeout time.Duration
}

type ERC20Transferer struct {
	client  EVMClient
	key     *ecdsa.PrivateKey
	from    ethcommon.Address
	token   ethcommon.Address
	poll    time.Duration
	timeout time.Duration
}

type TransactionOption func(*transactionOptions)

type transactionOptions struct {
	poll    time.Duration
	timeout time.Duration
}

func WithReceiptTimeout(timeout time.Duration) TransactionOption {
	return func(o *transactionOptions) {
		if timeout > 0 {
			o.timeout = timeout
		}
	}
}

func WithReceiptPollInterval(interval time.Duration) TransactionOption {
	return func(o *transactionOptions) {
		if interval > 0 {
			o.poll = interval
		}
	}
}

func NewEIP3009Capturer(client EVMClient, key *ecdsa.PrivateKey, usdc ethcommon.Address, opts ...TransactionOption) *EIP3009Capturer {
	from := ethcommon.Address{}
	if key != nil {
		from = crypto.PubkeyToAddress(key.PublicKey)
	}
	options := applyTransactionOptions(opts)
	return &EIP3009Capturer{client: client, key: key, from: from, usdc: usdc, poll: options.poll, timeout: options.timeout}
}

func NewERC20Transferer(client EVMClient, key *ecdsa.PrivateKey, token ethcommon.Address, opts ...TransactionOption) *ERC20Transferer {
	from := ethcommon.Address{}
	if key != nil {
		from = crypto.PubkeyToAddress(key.PublicKey)
	}
	options := applyTransactionOptions(opts)
	return &ERC20Transferer{client: client, key: key, from: from, token: token, poll: options.poll, timeout: options.timeout}
}

func applyTransactionOptions(opts []TransactionOption) transactionOptions {
	options := transactionOptions{poll: time.Second, timeout: 2 * time.Minute}
	for _, opt := range opts {
		opt(&options)
	}
	return options
}

func (c *EIP3009Capturer) Submit(ctx context.Context, auth TransferWithAuthorization, sig []byte) (ethcommon.Hash, error) {
	if c == nil || c.client == nil || c.key == nil || c.usdc == (ethcommon.Address{}) {
		return ethcommon.Hash{}, errors.New("x402.EIP3009Capturer: not configured")
	}
	data, err := PackTransferWithAuthorization(auth, sig)
	if err != nil {
		return ethcommon.Hash{}, err
	}
	return sendTokenTransaction(ctx, c.client, c.key, c.from, c.usdc, data, c.poll, c.timeout, "x402.EIP3009Capturer", "transferWithAuthorization")
}

func PackTransferWithAuthorization(auth TransferWithAuthorization, sig []byte) ([]byte, error) {
	if len(sig) != signatureLen {
		return nil, fmt.Errorf("x402.PackTransferWithAuthorization: sig length %d != %d", len(sig), signatureLen)
	}
	parsed, err := abi.JSON(strings.NewReader(`[{"type":"function","name":"transferWithAuthorization","inputs":[{"name":"from","type":"address"},{"name":"to","type":"address"},{"name":"value","type":"uint256"},{"name":"validAfter","type":"uint256"},{"name":"validBefore","type":"uint256"},{"name":"nonce","type":"bytes32"},{"name":"v","type":"uint8"},{"name":"r","type":"bytes32"},{"name":"s","type":"bytes32"}],"outputs":[]}]`))
	if err != nil {
		return nil, err
	}
	var r, s [32]byte
	copy(r[:], sig[:32])
	copy(s[:], sig[32:64])
	v := sig[64]
	if v < 27 {
		v += 27
	}
	return parsed.Pack("transferWithAuthorization", auth.From, auth.To, auth.Value, new(big.Int).SetUint64(auth.ValidAfter), new(big.Int).SetUint64(auth.ValidBefore), auth.Nonce, v, r, s)
}

func (t *ERC20Transferer) Transfer(ctx context.Context, to ethcommon.Address, amount *big.Int) (ethcommon.Hash, error) {
	if t == nil || t.client == nil || t.key == nil || t.token == (ethcommon.Address{}) {
		return ethcommon.Hash{}, errors.New("x402.ERC20Transferer: not configured")
	}
	if to == (ethcommon.Address{}) || amount == nil || amount.Sign() <= 0 {
		return ethcommon.Hash{}, errors.New("x402.ERC20Transferer: invalid transfer")
	}
	data, err := PackERC20Transfer(to, amount)
	if err != nil {
		return ethcommon.Hash{}, err
	}
	return sendTokenTransaction(ctx, t.client, t.key, t.from, t.token, data, t.poll, t.timeout, "x402.ERC20Transferer", "transfer")
}

func PackERC20Transfer(to ethcommon.Address, amount *big.Int) ([]byte, error) {
	parsed, err := abi.JSON(strings.NewReader(`[{"type":"function","name":"transfer","inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"type":"bool"}]}]`))
	if err != nil {
		return nil, err
	}
	return parsed.Pack("transfer", to, amount)
}

func sendTokenTransaction(ctx context.Context, client EVMClient, key *ecdsa.PrivateKey, from ethcommon.Address, token ethcommon.Address, data []byte, poll time.Duration, timeout time.Duration, label string, revertAction string) (ethcommon.Hash, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return ethcommon.Hash{}, fmt.Errorf("%s: nonce: %w", label, err)
	}
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return ethcommon.Hash{}, fmt.Errorf("%s: gas price: %w", label, err)
	}
	gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{From: from, To: &token, Value: big.NewInt(0), Data: data})
	if err != nil {
		return ethcommon.Hash{}, fmt.Errorf("%s: estimate gas: %w", label, err)
	}
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return ethcommon.Hash{}, fmt.Errorf("%s: chain id: %w", label, err)
	}
	tx := types.NewTransaction(nonce, token, big.NewInt(0), gasLimit, gasPrice, data)
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), key)
	if err != nil {
		return ethcommon.Hash{}, fmt.Errorf("%s: sign tx: %w", label, err)
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		return ethcommon.Hash{}, fmt.Errorf("%s: send tx: %w", label, err)
	}
	for {
		receipt, err := client.TransactionReceipt(ctx, signed.Hash())
		if err == nil {
			if receipt.Status != types.ReceiptStatusSuccessful {
				return ethcommon.Hash{}, fmt.Errorf("%s: %s reverted", label, revertAction)
			}
			return signed.Hash(), nil
		}
		if !errors.Is(err, ethereum.NotFound) {
			return signed.Hash(), nil
		}
		select {
		case <-ctx.Done():
			return signed.Hash(), nil
		case <-time.After(poll):
		}
	}
}
