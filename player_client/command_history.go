package client

type HistoryRing struct {
	buffer []string
	head   int
	size   int
}

func NewHistoryRing(capacity int) *HistoryRing {
	return &HistoryRing{
		buffer: make([]string, capacity),
		head:   0,
		size:   0,
	}
}

func (h *HistoryRing) Push(item string) {
	if h.size == len(h.buffer) {
		// Overwrite
		h.buffer[h.head] = item
		h.head += 1
		h.head %= len(h.buffer)
	} else {
		end := (h.head + h.size) % len(h.buffer)
		h.buffer[end] = item
		h.size += 1
	}
}

func (h *HistoryRing) Len() int {
	return h.size
}

// Represents the currently being shown index + 1.
type HistoryPos int

func (p *HistoryPos) CurrentItemPos() int {
	return int(*p) - 1
}

func (p *HistoryPos) Reset() {
	*p = 0
}

func clampInt(n, lo, hi int) int {
	if n >= hi {
		n = hi
	}
	if n <= lo {
		n = lo
	}
	return n
}

func (p *HistoryPos) GetOlder(r *HistoryRing) (string, bool) {
	cp := p.CurrentItemPos()
	onReset := false
	if cp == -1 {
		cp = 0
		onReset = true
	}

	if cp == r.size {
		return "<START_OF_HISTORY>", false
	}

	nextCp := cp + 1
	if onReset {
		nextCp = cp
	}

	cp = clampInt(nextCp, 0, r.size-1)
	*p = HistoryPos(cp + 1)

	bufLen := len(r.buffer)
	index := (r.head + r.size - 1 - cp) % bufLen

	return r.buffer[index], true
}

func (p *HistoryPos) GetNewer(r *HistoryRing) (string, bool) {
	cp := p.CurrentItemPos()
	if cp <= 0 {
		return "<END_OF_HISTORY>", false
	}

	cp = clampInt(cp-1, 0, r.size-1)
	*p = HistoryPos(cp + 1)

	bufLen := len(r.buffer)
	index := (r.head + r.size - 1 - cp) % bufLen
	return r.buffer[index], true
}

func (p *HistoryPos) Push(r *HistoryRing, item string) {
	p.Reset()
	r.Push(item)
}
