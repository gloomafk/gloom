package ui

import (
	"slices"
	"strings"
	"time"

	"gloom.im/gloom/internal/irc"
)

const namesTimeout = 30 * time.Second

// NAMES has no request IDs. Match each channel's replies in request order,
// including the automatic reply on JOIN and explicit requests from the user.
type namesRequest struct {
	at        time.Time
	automatic bool
	batch     *namesBatch
}

// Keep each aggregate completion tied to its original explicit command, even
// when the server also sends individual channel completions before it.
type namesBatch struct {
	at     time.Time
	target string
}

func (w *window) trackNames(request namesRequest) {
	w.namesRequests = append(w.namesRequests, request)
	w.suppressJoinNames = w.namesRequests[0].automatic
}

func (w *window) resetNames() {
	w.namesRequests = nil
	w.namesDirty = false
	w.namesReceiving = false
	w.suppressJoinNames = false
}

func (m *Model) refreshNames(w *window) {
	for _, request := range w.namesRequests {
		if request.automatic {
			w.namesDirty = true
			return
		}
	}
	if m.send("NAMES " + w.name) {
		w.trackNames(namesRequest{at: m.now, automatic: true})
	}
}

func (m *Model) trackExplicitNames(args []string) {
	request := namesRequest{at: m.now}
	if len(args) == 0 || strings.Contains(args[0], ",") {
		target := "*"
		if len(args) > 0 {
			target = irc.Fold(args[0])
		}
		request.batch = &namesBatch{at: m.now, target: target}
		m.namesBatches = append(m.namesBatches, request.batch)
	}
	if len(args) == 0 {
		for _, w := range m.windows {
			if isChannel(w.name) {
				w.trackNames(request)
			}
		}
		return
	}
	for target := range strings.SplitSeq(args[0], ",") {
		if i := m.find(target); i >= 0 && isChannel(m.windows[i].name) {
			m.windows[i].trackNames(request)
		}
	}
}

func (m *Model) endNames(target string) {
	if target == "*" || strings.Contains(target, ",") {
		m.finishNamesBatch(target)
		return
	}
	if i, ok := m.channelWindow(target); ok {
		m.finishNames(m.windows[i])
	}
}

func (m *Model) finishNamesBatch(target string) {
	key := irc.Fold(target)
	i := slices.IndexFunc(m.namesBatches, func(batch *namesBatch) bool { return batch.target == key })
	if i < 0 {
		return
	}
	batch := m.namesBatches[i]
	m.namesBatches = slices.Delete(m.namesBatches, i, i+1)
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
		w.suppressJoinNames = len(w.namesRequests) > 0 && w.namesRequests[0].automatic
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
	w.suppressJoinNames = len(w.namesRequests) > 0 && w.namesRequests[0].automatic
	return automatic
}

func (m *Model) expireNames() {
	m.namesBatches = slices.DeleteFunc(m.namesBatches, func(batch *namesBatch) bool {
		return m.now.Sub(batch.at) >= namesTimeout
	})
	for _, w := range m.windows {
		automatic := false
		for len(w.namesRequests) > 0 && m.now.Sub(w.namesRequests[0].at) >= namesTimeout {
			// Forget stalled replies so they cannot suppress requests forever.
			// A late reply is indistinguishable from a newer one on IRC.
			automatic = w.popNames() || automatic
		}
		if automatic && w.namesDirty {
			w.namesDirty = false
			m.refreshNames(w)
		}
	}
}
