package iforest

import (
	"math"
	"math/rand"
)

// Isolation Forest for 1D data (scores only), suitable for simple anomaly detection on metrics.
// Reference: Liu et al. (2008). This is a minimal, non-optimized implementation.

type Tree struct {
	Split float64
	Left  *Tree
	Right *Tree
	Leaf  bool
	Depth int
}

type Forest struct {
	Trees []*Tree
	C     float64 // average path length normalization factor
}

// averagePathLength computes c(n) ~ 2H(n-1) - 2(n-1)/n where H is harmonic number
func averagePathLength(n int) float64 {
	if n <= 1 {
		return 0
	}
	// Harmonic number approximation
	h := 0.0
	for i := 1; i < n; i++ {
		h += 1.0 / float64(i)
	}
	return 2*h - 2*float64(n-1)/float64(n)
}

// fitTree builds a random isolation tree on a subsample
func fitTree(data []float64, depth, maxDepth int) *Tree {
	if depth >= maxDepth || len(data) <= 1 {
		return &Tree{Leaf: true, Depth: depth}
	}
	minV, maxV := data[0], data[0]
	for _, v := range data {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if minV == maxV {
		return &Tree{Leaf: true, Depth: depth}
	}
	split := minV + rand.Float64()*(maxV-minV)
	left := make([]float64, 0, len(data))
	right := make([]float64, 0, len(data))
	for _, v := range data {
		if v < split {
			left = append(left, v)
		} else {
			right = append(right, v)
		}
	}
	return &Tree{
		Split: split,
		Left:  fitTree(left, depth+1, maxDepth),
		Right: fitTree(right, depth+1, maxDepth),
		Leaf:  false,
		Depth: depth,
	}
}

// New builds an isolation forest with t trees, each trained on a subsample of size psi (<= len(data))
func New(data []float64, t, psi int) *Forest {
	if psi <= 0 || psi > len(data) {
		psi = len(data)
	}
	trees := make([]*Tree, t)
	maxDepth := int(math.Ceil(math.Log2(float64(psi))))
	subsample := make([]float64, psi)
	for i := 0; i < t; i++ {
		for j := 0; j < psi; j++ {
			subsample[j] = data[rand.Intn(len(data))]
		}
		trees[i] = fitTree(subsample, 0, maxDepth)
	}
	return &Forest{Trees: trees, C: averagePathLength(psi)}
}

// pathLength computes expected path length for a point x across the forest
func (f *Forest) pathLength(x float64) float64 {
	pl := 0.0
	for _, t := range f.Trees {
		pl += pathLenTree(t, x)
	}
	return pl / float64(len(f.Trees))
}

func pathLenTree(t *Tree, x float64) float64 {
	if t.Leaf || t.Left == nil || t.Right == nil {
		return float64(t.Depth)
	}
	if x < t.Split {
		return pathLenTree(t.Left, x)
	}
	return pathLenTree(t.Right, x)
}

// Score returns anomaly score in [0,1], higher means more anomalous.
func (f *Forest) Score(x float64) float64 {
	if f.C == 0 {
		return 0
	}
	E := f.pathLength(x)
	return math.Pow(2, -E/f.C)
}
