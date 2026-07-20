package tui

type inputHistory struct {
	entries    []string
	cursor     int
	savedDraft string
}

func newInputHistory() inputHistory { return inputHistory{cursor: -1} }

func (h *inputHistory) replace(entries []string) {
	h.entries = append(h.entries[:0], entries...)
	h.cursor = -1
	h.savedDraft = ""
}

func (h *inputHistory) add(value string) {
	if value == "" || len(h.entries) > 0 && h.entries[len(h.entries)-1] == value {
		return
	}
	h.entries = append(h.entries, value)
	h.cursor = -1
	h.savedDraft = ""
}

func (h *inputHistory) previous(draft string) (string, bool) {
	if len(h.entries) == 0 {
		return draft, false
	}
	if h.cursor == -1 {
		h.savedDraft = draft
		h.cursor = len(h.entries) - 1
	} else if h.cursor > 0 {
		h.cursor--
	} else {
		return draft, false
	}
	return h.entries[h.cursor], true
}

func (h *inputHistory) next(current string) (string, bool) {
	if h.cursor == -1 {
		return current, false
	}
	if h.cursor < len(h.entries)-1 {
		h.cursor++
		return h.entries[h.cursor], true
	}
	h.cursor = -1
	return h.savedDraft, true
}

func (h *inputHistory) edit() {
	if h.cursor != -1 {
		h.cursor = -1
		h.savedDraft = ""
	}
}
