package money

import (
	"errors"
	"math/big"
)

var ErrOverflow = errors.New("money calculation exceeds int64")

func Mul(a, b int64) (int64, error) {
	v := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	if !v.IsInt64() {
		return 0, ErrOverflow
	}
	return v.Int64(), nil
}

func Sum(values ...int64) (int64, error) {
	v := new(big.Int)
	for _, n := range values {
		v.Add(v, big.NewInt(n))
	}
	if !v.IsInt64() {
		return 0, ErrOverflow
	}
	return v.Int64(), nil
}

// Prorate computes amount*remaining/total with half-up rounding and rejects overflow.
func Prorate(amount, remaining, total int64) (int64, error) {
	if amount <= 0 || remaining <= 0 || total <= 0 {
		return 0, nil
	}
	n := new(big.Int).Mul(big.NewInt(amount), big.NewInt(remaining))
	n.Add(n, new(big.Int).Div(big.NewInt(total), big.NewInt(2)))
	n.Div(n, big.NewInt(total))
	if !n.IsInt64() {
		return 0, ErrOverflow
	}
	return n.Int64(), nil
}
