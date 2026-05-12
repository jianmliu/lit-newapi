package x402auth

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const signatureLen = 65
const nonceReplayWindow = 24 * time.Hour

var (
	ErrAuthorizationExpired   = errors.New("x402: EIP-3009 authorization expired")
	ErrAuthorizationNotYet    = errors.New("x402: EIP-3009 authorization not yet valid")
	ErrAuthorizationReplay    = errors.New("x402: EIP-3009 authorization nonce replay")
	ErrAuthorizationSigner    = errors.New("x402: EIP-3009 signer mismatch")
	ErrAuthorizationRecipient = errors.New("x402: EIP-3009 recipient mismatch")
	ErrAuthorizationAmount    = errors.New("x402: EIP-3009 amount mismatch")
	ErrAuthorizationSignature = errors.New("x402: invalid EIP-3009 signature")
)

var transferWithAuthorizationTypehash = ethcommon.HexToHash("0x7c7c6cdb67a18743f49ec6fa9b35f50d52ed05cbed4cc592e13b44501c1a2267")
var eip712DomainTypehash = ethcommon.HexToHash("0x8b73c3c69bb8fe3d512ecc4cf759cc79239f7b179b0ffacaa9a75d522b39400f")

type AuthorizationDomain struct {
	Name              string
	Version           string
	ChainID           *big.Int
	VerifyingContract ethcommon.Address
}

type TransferWithAuthorization struct {
	From        ethcommon.Address
	To          ethcommon.Address
	Value       *big.Int
	ValidAfter  uint64
	ValidBefore uint64
	Nonce       [32]byte
}

type VerifyOptions struct {
	ExpectedFrom  ethcommon.Address
	ExpectedTo    ethcommon.Address
	ExpectedValue *big.Int
	Now           time.Time
}

type NonceStore struct {
	mu   sync.Mutex
	used map[ethcommon.Address]map[[32]byte]time.Time
}

func NewNonceStore() *NonceStore {
	return &NonceStore{used: make(map[ethcommon.Address]map[[32]byte]time.Time)}
}

func (s *NonceStore) MarkUsed(from ethcommon.Address, nonce [32]byte) error {
	return s.MarkUsedAt(from, nonce, time.Now())
}

func (s *NonceStore) MarkUsedAt(from ethcommon.Address, nonce [32]byte, now time.Time) error {
	if s == nil {
		return errors.New("x402: nil nonce store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	byNonce, ok := s.used[from]
	if !ok {
		byNonce = make(map[[32]byte]time.Time)
		s.used[from] = byNonce
	}
	for n, usedAt := range byNonce {
		if !usedAt.After(now.Add(-nonceReplayWindow)) {
			delete(byNonce, n)
		}
	}
	if usedAt, exists := byNonce[nonce]; exists && usedAt.After(now.Add(-nonceReplayWindow)) {
		return ErrAuthorizationReplay
	}
	byNonce[nonce] = now
	return nil
}

func VerifyTransferWithAuthorization(domain AuthorizationDomain, auth TransferWithAuthorization, sig []byte, opts VerifyOptions) (ethcommon.Address, error) {
	if auth.Value == nil || auth.Value.Sign() <= 0 {
		return ethcommon.Address{}, ErrAuthorizationAmount
	}
	if opts.ExpectedValue != nil && auth.Value.Cmp(opts.ExpectedValue) != 0 {
		return ethcommon.Address{}, ErrAuthorizationAmount
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	nowUnix := uint64(now.Unix())
	if nowUnix <= auth.ValidAfter {
		return ethcommon.Address{}, ErrAuthorizationNotYet
	}
	if nowUnix >= auth.ValidBefore {
		return ethcommon.Address{}, ErrAuthorizationExpired
	}
	if opts.ExpectedFrom != (ethcommon.Address{}) && auth.From != opts.ExpectedFrom {
		return ethcommon.Address{}, ErrAuthorizationSigner
	}
	if opts.ExpectedTo != (ethcommon.Address{}) && auth.To != opts.ExpectedTo {
		return ethcommon.Address{}, ErrAuthorizationRecipient
	}
	digest, err := domain.Digest(&auth)
	if err != nil {
		return ethcommon.Address{}, err
	}
	signer, err := recoverSigner(digest, sig)
	if err != nil {
		return ethcommon.Address{}, ErrAuthorizationSignature
	}
	if signer != auth.From {
		return ethcommon.Address{}, ErrAuthorizationSigner
	}
	return signer, nil
}

func (d AuthorizationDomain) Digest(auth *TransferWithAuthorization) ([32]byte, error) {
	hashStruct, err := auth.HashStruct()
	if err != nil {
		return [32]byte{}, err
	}
	sep, err := d.Separator()
	if err != nil {
		return [32]byte{}, err
	}
	preimage := make([]byte, 0, 66)
	preimage = append(preimage, 0x19, 0x01)
	preimage = append(preimage, sep[:]...)
	preimage = append(preimage, hashStruct[:]...)
	var out [32]byte
	copy(out[:], crypto.Keccak256(preimage))
	return out, nil
}

func (d AuthorizationDomain) Separator() ([32]byte, error) {
	if d.ChainID == nil || d.ChainID.Sign() <= 0 || d.VerifyingContract == (ethcommon.Address{}) {
		return [32]byte{}, errors.New("x402: invalid authorization domain")
	}
	args, err := domainArgs()
	if err != nil {
		return [32]byte{}, err
	}
	var nameHash [32]byte
	copy(nameHash[:], crypto.Keccak256([]byte(d.Name)))
	var versionHash [32]byte
	copy(versionHash[:], crypto.Keccak256([]byte(d.Version)))
	encoded, err := args.Pack(eip712DomainTypehash, nameHash, versionHash, d.ChainID, d.VerifyingContract)
	if err != nil {
		return [32]byte{}, fmt.Errorf("x402.AuthorizationDomain.Separator: pack: %w", err)
	}
	var out [32]byte
	copy(out[:], crypto.Keccak256(encoded))
	return out, nil
}

func (a *TransferWithAuthorization) HashStruct() ([32]byte, error) {
	if a == nil || a.Value == nil {
		return [32]byte{}, errors.New("x402.TransferWithAuthorization.HashStruct: nil arg")
	}
	args, err := transferArgs()
	if err != nil {
		return [32]byte{}, err
	}
	encoded, err := args.Pack(transferWithAuthorizationTypehash, a.From, a.To, a.Value, new(big.Int).SetUint64(a.ValidAfter), new(big.Int).SetUint64(a.ValidBefore), a.Nonce)
	if err != nil {
		return [32]byte{}, fmt.Errorf("x402.TransferWithAuthorization.HashStruct: pack: %w", err)
	}
	var out [32]byte
	copy(out[:], crypto.Keccak256(encoded))
	return out, nil
}

func recoverSigner(digest [32]byte, sig []byte) (ethcommon.Address, error) {
	if len(sig) != signatureLen {
		return ethcommon.Address{}, fmt.Errorf("recoverSigner: sig length %d != %d", len(sig), signatureLen)
	}
	canonical := make([]byte, signatureLen)
	copy(canonical, sig)
	if canonical[64] >= 27 {
		canonical[64] -= 27
	}
	if canonical[64] != 0 && canonical[64] != 1 {
		return ethcommon.Address{}, fmt.Errorf("recoverSigner: invalid V byte %d", sig[64])
	}
	r := new(big.Int).SetBytes(canonical[:32])
	s := new(big.Int).SetBytes(canonical[32:64])
	if !crypto.ValidateSignatureValues(canonical[64], r, s, true) {
		return ethcommon.Address{}, errors.New("recoverSigner: non-canonical signature")
	}
	pub, err := crypto.SigToPub(digest[:], canonical)
	if err != nil {
		return ethcommon.Address{}, err
	}
	return crypto.PubkeyToAddress(*pub), nil
}

func transferArgs() (abi.Arguments, error) {
	bytes32Ty, err := abi.NewType("bytes32", "", nil)
	if err != nil {
		return nil, err
	}
	addressTy, err := abi.NewType("address", "", nil)
	if err != nil {
		return nil, err
	}
	uint256Ty, err := abi.NewType("uint256", "", nil)
	if err != nil {
		return nil, err
	}
	return abi.Arguments{{Type: bytes32Ty}, {Type: addressTy}, {Type: addressTy}, {Type: uint256Ty}, {Type: uint256Ty}, {Type: uint256Ty}, {Type: bytes32Ty}}, nil
}

func domainArgs() (abi.Arguments, error) {
	bytes32Ty, err := abi.NewType("bytes32", "", nil)
	if err != nil {
		return nil, err
	}
	uint256Ty, err := abi.NewType("uint256", "", nil)
	if err != nil {
		return nil, err
	}
	addressTy, err := abi.NewType("address", "", nil)
	if err != nil {
		return nil, err
	}
	return abi.Arguments{{Type: bytes32Ty}, {Type: bytes32Ty}, {Type: bytes32Ty}, {Type: uint256Ty}, {Type: addressTy}}, nil
}
