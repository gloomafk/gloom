package ui

import (
	"slices"
	"strconv"
	"time"
)

const namesTimeout = 30 * time.Second

// NAMES has no request IDs. Match each channel's replies in request order,
// including the automatic reply on JOIN and explicit requests from the user.
type namesRequest struct {
	at        time.Time
	seq       uint64
	automatic bool
	batch     *namesBatch
}

// Keep each global completion tied to its original explicit command, even when
// the server also sends individual channel completions before it.
type namesBatch struct {
	at  time.Time
	seq uint64
}

type namesFence struct {
	token string
	seq   uint64
	at    time.Time
}

func (w *window) trackNames(request namesRequest) {
	w.namesRequests = append(w.namesRequests, request)
	w.suppressNames = w.namesRequests[0].automatic
}

func (w *window) resetNames() {
	w.namesRequests = nil
	w.namesDirty = false
	w.namesReceiving = false
	w.suppressNames = false
}

func (m *Model) refreshNames(w *window) {
	for _, request := range w.namesRequests {
		if request.automatic {
			w.namesDirty = true
			return
		}
	}
	if m.send("NAMES " + w.name) {
		w.trackNames(m.newNamesRequest(true, nil))
	}
}

func (m *Model) newNamesRequest(automatic bool, batch *namesBatch) namesRequest {
	m.namesSeq++
	return namesRequest{at: m.now, seq: m.namesSeq, automatic: automatic, batch: batch}
}

func (m *Model) trackExplicitNames(args []string) {
	if len(args) == 0 || args[0] == "*" {
		batch := &namesBatch{at: m.now}
		request := m.newNamesRequest(false, batch)
		batch.seq = request.seq
		m.namesBatches = append(m.namesBatches, batch)
		for _, w := range m.windows {
			if isChannel(w.name) {
				w.trackNames(request)
			}
		}
		return
	}
	target := args[0]
	if i := m.find(target); i >= 0 && isChannel(m.windows[i].name) {
		m.windows[i].trackNames(m.newNamesRequest(false, nil))
	}
}

func (m *Model) endNames(target string) {
	if target == "*" {
		m.finishNamesBatch()
		return
	}
	if i, ok := m.channelWindow(target); ok {
		m.finishNames(m.windows[i])
	}
}

func (m *Model) finishNamesBatch() {
	if len(m.namesBatches) == 0 {
		return
	}
	batch := m.namesBatches[0]
	m.namesBatches = slices.Delete(m.namesBatches, 0, 1)
	for _, w := range m.windows {
		if len(w.namesRequests) == 0 {
			continue
		}
		// A grouped completion must not consume a newer explicit request
		// or an automatic request waiting before or after this batch.
		if w.namesRequests[0].batch == batch {
			w.namesReceiving = false
		}
		w.namesRequests = slices.DeleteFunc(w.namesRequests, func(request namesRequest) bool {
			return request.batch == batch
		})
		w.suppressNames = len(w.namesRequests) > 0 && w.namesRequests[0].automatic
	}
}

func (m *Model) finishNames(w *window) {
	automatic := w.popNames()
	if automatic && w.namesDirty {
		w.namesDirty = false
		m.refreshNames(w)
	}
}

func (w *window) popNames() bool {
	automatic := false
	if len(w.namesRequests) > 0 {
		automatic = w.namesRequests[0].automatic
		w.namesRequests = slices.Delete(w.namesRequests, 0, 1)
	}
	w.namesReceiving = false
	w.suppressNames = len(w.namesRequests) > 0 && w.namesRequests[0].automatic
	return automatic
}

func (m *Model) expireNames() {
	if m.namesFence != nil && m.now.Sub(m.namesFence.at) >= namesTimeout {
		m.namesFence = nil
	}
	if m.namesFence != nil {
		return
	}
	var seq uint64
	var at time.Time
	consider := func(requestSeq uint64, requestAt time.Time) {
		if at.IsZero() || requestSeq < seq {
			seq, at = requestSeq, requestAt
		}
	}
	for _, w := range m.windows {
		if len(w.namesRequests) == 0 {
			continue
		}
		request := w.namesRequests[0]
		consider(request.seq, request.at)
	}
	for _, batch := range m.namesBatches {
		consider(batch.seq, batch.at)
	}
	if at.IsZero() || m.now.Sub(at) < namesTimeout {
		return
	}
	token := "names-" + strconv.FormatUint(m.namesFenceID+1, 10)
	if !m.send("PING :" + token) {
		return
	}
	m.namesFenceID++
	m.namesFence = &namesFence{token: token, seq: seq, at: m.now}
}

func (m *Model) finishNamesFence() {
	if m.namesFence == nil {
		return
	}
	fence := m.namesFence
	m.namesFence = nil
	m.namesBatches = slices.DeleteFunc(m.namesBatches, func(batch *namesBatch) bool {
		return batch.seq <= fence.seq
	})
	for _, w := range m.windows {
		count := 0
		automatic := false
		for count < len(w.namesRequests) && w.namesRequests[count].seq <= fence.seq {
			automatic = automatic || w.namesRequests[count].automatic
			count++
		}
		if count == 0 {
			continue
		}
		w.namesRequests = slices.Delete(w.namesRequests, 0, count)
		w.namesReceiving = false
		w.suppressNames = len(w.namesRequests) > 0 && w.namesRequests[0].automatic
		if automatic && w.namesDirty {
			w.namesDirty = false
			m.refreshNames(w)
		}
	}
}
