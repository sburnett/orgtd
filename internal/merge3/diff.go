package merge3

import "bytes"

// diffHunks returns the changes that turn a into b, as hunks in order of
// position in a, each tagged with s. It's a Myers diff (O((N+M)·D)) over
// whole lines, after trimming the common prefix and suffix, which is all
// an ordinary edit leaves to compare.
func diffHunks(a, b [][]byte, s side) []hunk {
	prefix := 0
	for prefix < len(a) && prefix < len(b) && bytes.Equal(a[prefix], b[prefix]) {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix &&
		bytes.Equal(a[len(a)-1-suffix], b[len(b)-1-suffix]) {
		suffix++
	}
	a, b = a[prefix:len(a)-suffix], b[prefix:len(b)-suffix]

	// Compare lines as small integers rather than byte slices.
	ids := map[string]int{}
	intern := func(lines [][]byte) []int {
		out := make([]int, len(lines))
		for i, l := range lines {
			id, ok := ids[string(l)]
			if !ok {
				id = len(ids)
				ids[string(l)] = id
			}
			out[i] = id
		}
		return out
	}
	x, y := intern(a), intern(b)

	var hunks []hunk
	ai, bi := 0, 0
	flush := func(aEnd, bEnd int) {
		if ai < aEnd || bi < bEnd {
			hunks = append(hunks, hunk{side: s, baseStart: prefix + ai, baseEnd: prefix + aEnd, start: prefix + bi, end: prefix + bEnd})
		}
	}
	for _, m := range matches(x, y) {
		flush(m[0], m[1])
		ai, bi = m[0]+1, m[1]+1
	}
	flush(len(x), len(y))
	return hunks
}

// matches returns the pairs (i, j) of a longest common subsequence of x
// and y, with x[i] == y[j], in increasing order — Myers' greedy
// algorithm, keeping each round's furthest-reaching paths to trace the
// result back.
func matches(x, y []int) [][2]int {
	n, m := len(x), len(y)
	if n == 0 || m == 0 {
		return nil
	}
	maxD := n + m
	offset := maxD + 1
	v := make([]int, 2*maxD+3)
	var trace [][]int
	found := false
	for d := 0; d <= maxD && !found; d++ {
		snapshot := make([]int, 2*d+3)
		for k := -d; k <= d; k += 2 {
			var px int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				px = v[offset+k+1] // down: an insertion from y
			} else {
				px = v[offset+k-1] + 1 // right: a deletion from x
			}
			py := px - k
			for px < n && py < m && x[px] == y[py] {
				px++
				py++
			}
			v[offset+k] = px
			if px >= n && py >= m {
				found = true
			}
		}
		// Keep only the diagonals round d could have reached.
		for k := -d - 1; k <= d+1; k++ {
			snapshot[k+d+1] = v[offset+k]
		}
		trace = append(trace, snapshot)
	}

	// Walk back from (n, m) through each round's snapshot.
	get := func(d, k int) int {
		s := trace[d]
		i := k + d + 1
		if i < 0 || i >= len(s) {
			return -1
		}
		return s[i]
	}
	var out [][2]int
	px, py := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		k := px - py
		var prevK int
		if d == 0 {
			prevK = 0
		} else if k == -d || (k != d && get(d-1, k-1) < get(d-1, k+1)) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		startX := 0
		if d > 0 {
			startX = get(d-1, prevK)
			if prevK == k-1 {
				startX++
			}
		}
		startY := startX - k
		// The snake: matching lines from the step's end back to (startX, startY).
		for px > startX && py > startY {
			px--
			py--
			out = append(out, [2]int{px, py})
		}
		if d > 0 {
			px = get(d-1, prevK)
			py = px - prevK
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
