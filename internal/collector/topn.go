package collector

import (
	"sort"

	"github.com/overstarry/sloth/internal/model"
)

// topByMean returns the n slowest statements by mean execution time.
func topByMean(in []model.SlowSQL, n int) []model.SlowSQL {
	sort.Slice(in, func(i, j int) bool {
		return in[i].MeanExecMs > in[j].MeanExecMs
	})
	if n > 0 && len(in) > n {
		in = in[:n]
	}
	return in
}
