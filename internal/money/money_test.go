package money

import (
	"math"
	"testing"
)

func TestProrateHalfUp(t *testing.T) {
	tests := []struct{ amount, remaining, total, want int64 }{{100, 5, 10, 50}, {99, 5, 10, 50}, {100, 0, 10, 0}, {100, 10, 10, 100}, {100, 1, 3, 33}}
	for _, tt := range tests {
		got, err := Prorate(tt.amount, tt.remaining, tt.total)
		if err != nil || got != tt.want {
			t.Fatalf("Prorate(%d,%d,%d)=%d,%v want %d", tt.amount, tt.remaining, tt.total, got, err, tt.want)
		}
	}
}
func TestOverflowRejected(t *testing.T) {
	if _, err := Mul(math.MaxInt64, 2); err == nil {
		t.Fatal("multiplication overflow accepted")
	}
	if _, err := Sum(math.MaxInt64, 1); err == nil {
		t.Fatal("sum overflow accepted")
	}
}

func BenchmarkInvoiceArithmetic(b *testing.B) {
	for i := 0; i < b.N; i++ {
		overage, err := Mul(90_000, 10)
		if err != nil {
			b.Fatal(err)
		}
		credit, err := Prorate(99_000, 1_200_000, 2_592_000)
		if err != nil {
			b.Fatal(err)
		}
		charge, err := Prorate(299_000, 1_200_000, 2_592_000)
		if err != nil {
			b.Fatal(err)
		}
		if _, err = Sum(299_000, overage, -credit, charge); err != nil {
			b.Fatal(err)
		}
	}
}
