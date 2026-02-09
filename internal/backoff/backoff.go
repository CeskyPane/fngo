package backoff

import "time"

func Exponential(attempt int, minDelay, maxDelay time.Duration) time.Duration {
	if minDelay <= 0 {
		minDelay = 100 * time.Millisecond
	}

	if maxDelay <= 0 {
		maxDelay = 5 * time.Second
	}

	if maxDelay < minDelay {
		maxDelay = minDelay
	}

	if attempt <= 0 {
		return minDelay
	}

	delay := minDelay
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay >= maxDelay {
			return maxDelay
		}
	}

	return delay
}
