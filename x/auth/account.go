package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/tendermint/tendermint/crypto"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Account is an interface used to store coins at a given address within state.
// It presumes a notion of sequence numbers for replay protection,
// a notion of account numbers for replay protection for previously pruned accounts,
// and a pubkey for authentication purposes.
//
// Many complex conditions can be used in the concrete struct which implements Account.
type Account interface {
	GetAddress() sdk.AccAddress
	SetAddress(sdk.AccAddress) error // errors if already set.

	GetPubKey() crypto.PubKey // can return nil.
	SetPubKey(crypto.PubKey) error

	GetAccountNumber() uint64
	SetAccountNumber(uint64) error

	GetSequence() uint64
	SetSequence(uint64) error

	GetCoins() sdk.Coins
	SetCoins(sdk.Coins) error

	// Calculates the amount of coins that can be sent to other accounts given
	// the current time.
	SpendableCoins(blockTime time.Time) sdk.Coins

	// Ensure that account implements stringer
	String() string
}

// VestingAccount defines an account type that vests coins via a vesting schedule.
type VestingAccount interface {
	Account

	// Delegation and undelegation accounting that returns the resulting base
	// coins amount.
	TrackDelegation(blockTime time.Time, amount sdk.Coins)
	TrackUndelegation(amount sdk.Coins)

	GetVestedCoins(blockTime time.Time) sdk.Coins
	GetVestingCoins(blockTime time.Time) sdk.Coins

	GetStartTime() int64
	GetEndTime() int64
}

// AccountDecoder unmarshals account bytes
type AccountDecoder func(accountBytes []byte) (Account, error)

//-----------------------------------------------------------------------------
// BaseAccount

var _ Account = (*BaseAccount)(nil)

// BaseAccount - a base account structure.
// This can be extended by embedding within in your AppAccount.
// There are examples of this in: examples/basecoin/types/account.go.
// However one doesn't have to use BaseAccount as long as your struct
// implements Account.
type BaseAccount struct {
	Address       sdk.AccAddress `json:"address"`
	Coins         sdk.Coins      `json:"coins"`
	PubKey        crypto.PubKey  `json:"public_key"`
	AccountNumber uint64         `json:"account_number"`
	Sequence      uint64         `json:"sequence"`
}

func (acc BaseAccount) String() string {
	return fmt.Sprintf(`Account %s:
  Coins:         %s
  PubKey:        %s
  AccountNumber: %d
  Sequence:      %d`, acc.Address, acc.Coins,
		acc.PubKey.Address(), acc.AccountNumber, acc.Sequence)
}

// Prototype function for BaseAccount
func ProtoBaseAccount() Account {
	return &BaseAccount{}
}

func NewBaseAccountWithAddress(addr sdk.AccAddress) BaseAccount {
	return BaseAccount{
		Address: addr,
	}
}

// Implements sdk.Account.
func (acc BaseAccount) GetAddress() sdk.AccAddress {
	return acc.Address
}

// Implements sdk.Account.
func (acc *BaseAccount) SetAddress(addr sdk.AccAddress) error {
	if len(acc.Address) != 0 {
		return errors.New("cannot override BaseAccount address")
	}
	acc.Address = addr
	return nil
}

// Implements sdk.Account.
func (acc BaseAccount) GetPubKey() crypto.PubKey {
	return acc.PubKey
}

// Implements sdk.Account.
func (acc *BaseAccount) SetPubKey(pubKey crypto.PubKey) error {
	acc.PubKey = pubKey
	return nil
}

// Implements sdk.Account.
func (acc *BaseAccount) GetCoins() sdk.Coins {
	return acc.Coins
}

// Implements sdk.Account.
func (acc *BaseAccount) SetCoins(coins sdk.Coins) error {
	acc.Coins = coins
	return nil
}

// Implements Account
func (acc *BaseAccount) GetAccountNumber() uint64 {
	return acc.AccountNumber
}

// Implements Account
func (acc *BaseAccount) SetAccountNumber(accNumber uint64) error {
	acc.AccountNumber = accNumber
	return nil
}

// Implements sdk.Account.
func (acc *BaseAccount) GetSequence() uint64 {
	return acc.Sequence
}

// Implements sdk.Account.
func (acc *BaseAccount) SetSequence(seq uint64) error {
	acc.Sequence = seq
	return nil
}

// SpendableCoins returns the total set of spendable coins. For a base account,
// this is simply the base coins.
func (acc *BaseAccount) SpendableCoins(_ time.Time) sdk.Coins {
	return acc.GetCoins()
}

//-----------------------------------------------------------------------------
// Base Vesting Account

// BaseVestingAccount implements the VestingAccount interface. It contains all
// the necessary fields needed for any vesting account implementation.
type BaseVestingAccount struct {
	*BaseAccount

	OriginalVesting  sdk.Coins // coins in account upon initialization
	DelegatedFree    sdk.Coins // coins that are vested and delegated
	DelegatedVesting sdk.Coins // coins that vesting and delegated

	EndTime int64 // when the coins become unlocked
}

func (bva BaseVestingAccount) String() string {
	return fmt.Sprintf(`Vesting Account %s:
  Coins:            %s
  PubKey:           %s
  AccountNumber:    %d
  Sequence:         %d
  OriginalVesting:  %s
  DelegatedFree:    %s
  DelegatedVesting: %s
  EndTime:          %d `, bva.Address, bva.Coins,
		bva.PubKey.Address(), bva.AccountNumber, bva.Sequence,
		bva.OriginalVesting, bva.DelegatedFree,
		bva.DelegatedVesting, bva.EndTime)
}

// spendableCoins returns all the spendable coins for a vesting account given a
// set of vesting coins.
//
// CONTRACT: The account's coins, delegated vesting coins, vestingCoins must be
// sorted.
func (bva BaseVestingAccount) spendableCoins(vestingCoins sdk.Coins) sdk.Coins {
	var spendableCoins sdk.Coins
	bc := bva.GetCoins()

	j, k := 0, 0
	for _, coin := range bc {
		// zip/lineup all coins by their denomination to provide O(n) time
		for j < len(vestingCoins) && vestingCoins[j].Denom != coin.Denom {
			j++
		}
		for k < len(bva.DelegatedVesting) && bva.DelegatedVesting[k].Denom != coin.Denom {
			k++
		}

		baseAmt := coin.Amount

		vestingAmt := sdk.ZeroInt()
		if len(vestingCoins) > 0 {
			vestingAmt = vestingCoins[j].Amount
		}

		delVestingAmt := sdk.ZeroInt()
		if len(bva.DelegatedVesting) > 0 {
			delVestingAmt = bva.DelegatedVesting[k].Amount
		}

		// compute min((BC + DV) - V, BC) per the specification
		min := sdk.MinInt(baseAmt.Add(delVestingAmt).Sub(vestingAmt), baseAmt)
		spendableCoin := sdk.NewCoin(coin.Denom, min)

		if !spendableCoin.IsZero() {
			spendableCoins = spendableCoins.Plus(sdk.Coins{spendableCoin})
		}
	}

	return spendableCoins
}

// trackDelegation tracks a delegation amount for any given vesting account type
// given the amount of coins currently vesting. It returns the resulting base
// coins.
//
// CONTRACT: The account's coins, delegation coins, vesting coins, and delegated
// vesting coins must be sorted.
func (bva *BaseVestingAccount) trackDelegation(vestingCoins, amount sdk.Coins) {
	bc := bva.GetCoins()

	i, j, k := 0, 0, 0
	for _, coin := range amount {
		// zip/lineup all coins by their denomination to provide O(n) time
		for i < len(bc) && bc[i].Denom != coin.Denom {
			i++
		}
		for j < len(vestingCoins) && vestingCoins[j].Denom != coin.Denom {
			j++
		}
		for k < len(bva.DelegatedVesting) && bva.DelegatedVesting[k].Denom != coin.Denom {
			k++
		}

		baseAmt := sdk.ZeroInt()
		if len(bc) > 0 {
			baseAmt = bc[i].Amount
		}

		vestingAmt := sdk.ZeroInt()
		if len(vestingCoins) > 0 {
			vestingAmt = vestingCoins[j].Amount
		}

		delVestingAmt := sdk.ZeroInt()
		if len(bva.DelegatedVesting) > 0 {
			delVestingAmt = bva.DelegatedVesting[k].Amount
		}

		// Panic if the delegation amount is zero or if the base coins does not
		// exceed the desired delegation amount.
		if coin.Amount.IsZero() || baseAmt.LT(coin.Amount) {
			panic("delegation attempt with zero coins or insufficient funds")
		}

		// compute x and y per the specification, where:
		// X := min(max(V - DV, 0), D)
		// Y := D - X
		x := sdk.MinInt(sdk.MaxInt(vestingAmt.Sub(delVestingAmt), sdk.ZeroInt()), coin.Amount)
		y := coin.Amount.Sub(x)

		if !x.IsZero() {
			xCoin := sdk.NewCoin(coin.Denom, x)
			bva.DelegatedVesting = bva.DelegatedVesting.Plus(sdk.Coins{xCoin})
		}

		if !y.IsZero() {
			yCoin := sdk.NewCoin(coin.Denom, y)
			bva.DelegatedFree = bva.DelegatedFree.Plus(sdk.Coins{yCoin})
		}

		bva.Coins = bva.Coins.Minus(sdk.Coins{coin})
	}
}

// TrackUndelegation tracks an undelegation amount by setting the necessary
// values by which delegated vesting and delegated vesting need to decrease and
// by which amount the base coins need to increase. The resulting base coins are
// returned.
//
// CONTRACT: The account's coins and undelegation coins must be sorted.
func (bva *BaseVestingAccount) TrackUndelegation(amount sdk.Coins) {
	i := 0
	for _, coin := range amount {
		// panic if the undelegation amount is zero
		if coin.Amount.IsZero() {
			panic("undelegation attempt with zero coins")
		}

		for i < len(bva.DelegatedFree) && bva.DelegatedFree[i].Denom != coin.Denom {
			i++
		}

		delegatedFree := sdk.ZeroInt()
		if len(bva.DelegatedFree) > 0 {
			delegatedFree = bva.DelegatedFree[i].Amount
		}

		// compute x and y per the specification, where:
		// X := min(DF, D)
		// Y := D - X
		x := sdk.MinInt(delegatedFree, coin.Amount)
		y := coin.Amount.Sub(x)

		if !x.IsZero() {
			xCoin := sdk.NewCoin(coin.Denom, x)
			bva.DelegatedFree = bva.DelegatedFree.Minus(sdk.Coins{xCoin})
		}

		if !y.IsZero() {
			yCoin := sdk.NewCoin(coin.Denom, y)
			bva.DelegatedVesting = bva.DelegatedVesting.Minus(sdk.Coins{yCoin})
		}

		bva.Coins = bva.Coins.Plus(sdk.Coins{coin})
	}
}

//-----------------------------------------------------------------------------
// Continuous Vesting Account

var _ VestingAccount = (*ContinuousVestingAccount)(nil)

// ContinuousVestingAccount implements the VestingAccount interface. It
// continuously vests by unlocking coins linearly with respect to time.
type ContinuousVestingAccount struct {
	*BaseVestingAccount

	StartTime int64 // when the coins start to vest
}

func NewContinuousVestingAccount(
	baseAcc *BaseAccount, StartTime, EndTime int64,
) *ContinuousVestingAccount {

	baseVestingAcc := &BaseVestingAccount{
		BaseAccount:     baseAcc,
		OriginalVesting: baseAcc.Coins,
		EndTime:         EndTime,
	}

	return &ContinuousVestingAccount{
		StartTime:          StartTime,
		BaseVestingAccount: baseVestingAcc,
	}
}

func (cva ContinuousVestingAccount) String() string {
	return fmt.Sprintf(`Vesting Account %s:
  Coins:            %s
  PubKey:           %s
  AccountNumber:    %d
  Sequence:         %d
  OriginalVesting:  %s
  DelegatedFree:    %s
  DelegatedVesting: %s
  StartTime:        %d
  EndTime:          %d `, cva.Address, cva.Coins,
		cva.PubKey.Address(), cva.AccountNumber, cva.Sequence,
		cva.OriginalVesting, cva.DelegatedFree,
		cva.DelegatedVesting, cva.StartTime, cva.EndTime)
}

// GetVestedCoins returns the total number of vested coins. If no coins are vested,
// nil is returned.
func (cva ContinuousVestingAccount) GetVestedCoins(blockTime time.Time) sdk.Coins {
	var vestedCoins sdk.Coins

	// We must handle the case where the start time for a vesting account has
	// been set into the future or when the start of the chain is not exactly
	// known.
	if blockTime.Unix() <= cva.StartTime {
		return vestedCoins
	} else if blockTime.Unix() >= cva.EndTime {
		return cva.OriginalVesting
	}

	// calculate the vesting scalar
	x := blockTime.Unix() - cva.StartTime
	y := cva.EndTime - cva.StartTime
	s := sdk.NewDec(x).Quo(sdk.NewDec(y))

	for _, ovc := range cva.OriginalVesting {
		vestedAmt := sdk.NewDecFromInt(ovc.Amount).Mul(s).RoundInt()
		vestedCoins = append(vestedCoins, sdk.NewCoin(ovc.Denom, vestedAmt))
	}

	return vestedCoins
}

// GetVestingCoins returns the total number of vesting coins. If no coins are
// vesting, nil is returned.
func (cva ContinuousVestingAccount) GetVestingCoins(blockTime time.Time) sdk.Coins {
	return cva.OriginalVesting.Minus(cva.GetVestedCoins(blockTime))
}

// SpendableCoins returns the total number of spendable coins per denom for a
// continuous vesting account.
func (cva ContinuousVestingAccount) SpendableCoins(blockTime time.Time) sdk.Coins {
	return cva.spendableCoins(cva.GetVestingCoins(blockTime))
}

// TrackDelegation tracks a desired delegation amount by setting the appropriate
// values for the amount of delegated vesting, delegated free, and reducing the
// overall amount of base coins.
func (cva *ContinuousVestingAccount) TrackDelegation(blockTime time.Time, amount sdk.Coins) {
	cva.trackDelegation(cva.GetVestingCoins(blockTime), amount)
}

// GetStartTime returns the time when vesting starts for a continuous vesting
// account.
func (cva *ContinuousVestingAccount) GetStartTime() int64 {
	return cva.StartTime
}

// GetEndTime returns the time when vesting ends for a continuous vesting account.
func (cva *ContinuousVestingAccount) GetEndTime() int64 {
	return cva.EndTime
}

//-----------------------------------------------------------------------------
// Delayed Vesting Account

var _ VestingAccount = (*DelayedVestingAccount)(nil)

// DelayedVestingAccount implements the VestingAccount interface. It vests all
// coins after a specific time, but non prior. In other words, it keeps them
// locked until a specified time.
type DelayedVestingAccount struct {
	*BaseVestingAccount
}

func NewDelayedVestingAccount(baseAcc *BaseAccount, EndTime int64) *DelayedVestingAccount {
	baseVestingAcc := &BaseVestingAccount{
		BaseAccount:     baseAcc,
		OriginalVesting: baseAcc.Coins,
		EndTime:         EndTime,
	}

	return &DelayedVestingAccount{baseVestingAcc}
}

// GetVestedCoins returns the total amount of vested coins for a delayed vesting
// account. All coins are only vested once the schedule has elapsed.
func (dva DelayedVestingAccount) GetVestedCoins(blockTime time.Time) sdk.Coins {
	if blockTime.Unix() >= dva.EndTime {
		return dva.OriginalVesting
	}

	return nil
}

// GetVestingCoins returns the total number of vesting coins for a delayed
// vesting account.
func (dva DelayedVestingAccount) GetVestingCoins(blockTime time.Time) sdk.Coins {
	return dva.OriginalVesting.Minus(dva.GetVestedCoins(blockTime))
}

// SpendableCoins returns the total number of spendable coins for a delayed
// vesting account.
func (dva DelayedVestingAccount) SpendableCoins(blockTime time.Time) sdk.Coins {
	return dva.spendableCoins(dva.GetVestingCoins(blockTime))
}

// TrackDelegation tracks a desired delegation amount by setting the appropriate
// values for the amount of delegated vesting, delegated free, and reducing the
// overall amount of base coins.
func (dva *DelayedVestingAccount) TrackDelegation(blockTime time.Time, amount sdk.Coins) {
	dva.trackDelegation(dva.GetVestingCoins(blockTime), amount)
}

// GetStartTime returns zero since a delayed vesting account has no start time.
func (dva *DelayedVestingAccount) GetStartTime() int64 {
	return 0
}

// GetEndTime returns the time when vesting ends for a delayed vesting account.
func (dva *DelayedVestingAccount) GetEndTime() int64 {
	return dva.EndTime
}

//-----------------------------------------------------------------------------
// Codec

// Most users shouldn't use this, but this comes in handy for tests.
func RegisterBaseAccount(cdc *codec.Codec) {
	cdc.RegisterInterface((*Account)(nil), nil)
	cdc.RegisterInterface((*VestingAccount)(nil), nil)
	cdc.RegisterConcrete(&BaseAccount{}, "cosmos-sdk/BaseAccount", nil)
	cdc.RegisterConcrete(&BaseVestingAccount{}, "cosmos-sdk/BaseVestingAccount", nil)
	cdc.RegisterConcrete(&ContinuousVestingAccount{}, "cosmos-sdk/ContinuousVestingAccount", nil)
	cdc.RegisterConcrete(&DelayedVestingAccount{}, "cosmos-sdk/DelayedVestingAccount", nil)
	codec.RegisterCrypto(cdc)
}
