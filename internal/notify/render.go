package notify

import (
	"errors"
	"unicode/utf8"
)

const truncationMarker = "… [truncated]"

// fitRenderedText bounds the fully encoded payload rather than the source
// string, accounting for JSON escaping and multibyte UTF-8.
func fitRenderedText(text string, maximumBytes int, render func(string) ([]byte, error)) ([]byte, bool, error) {
	if maximumBytes <= 0 {
		return nil, false, errors.New("maximum payload bytes must be positive")
	}
	full, err := render(text)
	if err != nil {
		return nil, false, err
	}
	if len(full) <= maximumBytes {
		return full, false, nil
	}

	minimal, err := render(truncationMarker)
	if err != nil {
		return nil, false, err
	}
	if len(minimal) > maximumBytes {
		return nil, false, errors.New("maximum payload bytes is too small for the protocol envelope")
	}

	runes := []rune(text)
	low, high := 0, len(runes)
	best := minimal
	for low <= high {
		middle := low + (high-low)/2
		candidateText := truncationMarker
		if middle > 0 {
			candidateText = string(runes[:middle]) + truncationMarker
		}
		candidate, renderErr := render(candidateText)
		if renderErr != nil {
			return nil, false, renderErr
		}
		if len(candidate) <= maximumBytes {
			best = candidate
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	if !utf8.Valid(best) {
		return nil, false, errors.New("rendered payload is not valid UTF-8")
	}
	return best, true, nil
}

func truncateRunes(text string, maximumRunes int) (string, bool) {
	if maximumRunes < 0 {
		maximumRunes = 0
	}
	runes := []rune(text)
	if len(runes) <= maximumRunes {
		return text, false
	}
	if maximumRunes == 0 {
		return truncationMarker, true
	}
	return string(runes[:maximumRunes]) + truncationMarker, true
}
