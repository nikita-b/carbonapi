package tukey

import (
	"container/heap"
	"errors"
	"github.com/go-graphite/carbonapi/expr/helper"
	"github.com/go-graphite/carbonapi/expr/interfaces"
	"github.com/go-graphite/carbonapi/expr/metadata"
	"github.com/go-graphite/carbonapi/expr/types"
	"github.com/go-graphite/carbonapi/pkg/parser"
	"sort"
	"strings"
)

func init() {
	f := &Function{}
	functions := []string{"tukeyAbove", "tukeyBelow"}
	for _, function := range functions {
		metadata.RegisterFunction(function, f)
	}
}

type Function struct {
	interfaces.FunctionBase
}

// tukeyAbove(seriesList,basis,n,interval=0) , tukeyBelow(seriesList,basis,n,interval=0)
func (f *Function) Do(e parser.Expr, from, until int32, values map[parser.MetricRequest][]*types.MetricData) ([]*types.MetricData, error) {
	arg, err := helper.GetSeriesArg(e.Args()[0], from, until, values)
	if err != nil {
		return nil, err
	}

	basis, err := e.GetFloatArg(1)
	if err != nil || basis <= 0 {
		return nil, err
	}

	n, err := e.GetIntArg(2)
	if err != nil {
		return nil, err
	}
	if n < 1 {
		return nil, errors.New("n must be larger or equal to 1")
	}

	var beginInterval int
	endInterval := len(arg[0].Values)
	if len(e.Args()) >= 4 {
		switch e.Args()[3].Type() {
		case parser.EtConst:
			beginInterval, err = e.GetIntArg(3)
		case parser.EtString:
			var i32 int32
			i32, err = e.GetIntervalArg(3, 1)
			beginInterval = int(i32)
			beginInterval /= int(arg[0].StepTime)
			// TODO(nnuss): make sure the arrays are all the same 'size'
		default:
			err = parser.ErrBadType
		}
		if err != nil {
			return nil, err
		}
		if beginInterval < 0 && (-1*beginInterval) < endInterval {
			// negative intervals are everything preceding the last 'interval' points
			endInterval += beginInterval
			beginInterval = 0
		} else if beginInterval > 0 && beginInterval < endInterval {
			// positive intervals are the last 'interval' points
			beginInterval = endInterval - beginInterval
			//endInterval = len(arg[0].Values)
		} else {
			// zero -or- beyond the len() of the series ; will revert to whole range
			beginInterval = 0
			//endInterval = len(arg[0].Values)
		}
	}

	// gather all the valid points
	var points []float64
	for _, a := range arg {
		for i, m := range a.Values[beginInterval:endInterval] {
			if a.IsAbsent[beginInterval+i] {
				continue
			}
			points = append(points, m)
		}
	}

	sort.Float64s(points)

	first := int(0.25 * float64(len(points)))
	third := int(0.75 * float64(len(points)))

	iqr := points[third] - points[first]

	max := points[third] + basis*iqr
	min := points[first] - basis*iqr

	isAbove := strings.HasSuffix(e.Target(), "Above")

	var mh types.MetricHeap

	// count how many points are above the threshold
	for i, a := range arg {
		var outlier int
		for i, m := range a.Values[beginInterval:endInterval] {
			if a.IsAbsent[beginInterval+i] {
				continue
			}
			if isAbove {
				if m >= max {
					outlier++
				}
			} else {
				if m <= min {
					outlier++
				}
			}
		}

		// not even a single anomalous point -- ignore this metric
		if outlier == 0 {
			continue
		}

		if len(mh) < n {
			heap.Push(&mh, types.MetricHeapElement{Idx: i, Val: float64(outlier)})
			continue
		}
		// current outlier count is is bigger than smallest max found so far
		foutlier := float64(outlier)
		if mh[0].Val < foutlier {
			mh[0].Val = foutlier
			mh[0].Idx = i
			heap.Fix(&mh, 0)
		}
	}

	if len(mh) < n {
		n = len(mh)
	}
	results := make([]*types.MetricData, n)
	// results should be ordered ascending
	for len(mh) > 0 {
		v := heap.Pop(&mh).(types.MetricHeapElement)
		results[len(mh)] = arg[v.Idx]
	}

	return results, nil
}