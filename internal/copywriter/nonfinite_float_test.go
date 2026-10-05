package copywriter

import (
	"math"
	"testing"
)

// numeric_text_to_double refuses a non-finite float64, so a REAL infinity or NaN
// cannot pass through it.
func TestNumericTextToDouble_RejectsNonFiniteFloat64(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := Transform("numeric_text_to_double", v); err == nil {
			t.Errorf("numeric_text_to_double(%v) succeeded, want an error", v)
		}
	}
}
