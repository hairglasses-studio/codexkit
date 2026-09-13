package researchrefresh

import (
	"fmt"
	"time"
)

// EvaluateDue determines whether feed should be fetched at now. A nil receipt
// is always due. Cadence advances from the last successful check, including a
// 304 response, so unchanged feeds do not hot-loop.
func EvaluateDue(feed Feed, previous *Receipt, now time.Time) (DueResult, error) {
	if err := validateFeed(feed); err != nil {
		return DueResult{}, fmt.Errorf("%w: feed: %v", ErrInvalidRegistry, err)
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	if previous == nil {
		return DueResult{Due: true}, nil
	}
	if err := ValidateReceiptForFeed(feed, *previous, now); err != nil {
		return DueResult{}, err
	}
	checkedAt := previous.CheckedAt.UTC()
	nextDueAt := checkedAt.Add(time.Duration(feed.CadenceHours) * time.Hour)
	return DueResult{
		Due:       !now.Before(nextDueAt),
		CheckedAt: checkedAt,
		NextDueAt: nextDueAt,
	}, nil
}
