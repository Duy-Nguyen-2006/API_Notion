package orchestrator

import (
	"context"
	"time"
)

type hedgedResult struct { result ModelResult; err error; streamed bool }

type discardWriter struct{}
func (discardWriter) WriteDelta(string) error { return nil }

func ChatStreamHedged(ctx context.Context, primary ModelClient, hedge ModelClient, delay time.Duration, messages []Message, w StreamWriter) (ModelResult, bool, error) {
	if primary == nil { return ModelResult{}, false, context.Canceled }
	if hedge == nil || delay <= 0 || hedge == primary { r, err := primary.ChatStream(ctx, messages, w); return r, false, err }
	child, cancel := context.WithCancel(ctx); defer cancel()
	ch := make(chan hedgedResult, 2)
	go func() { r, err := primary.ChatStream(child, messages, w); ch <- hedgedResult{result: r, err: err, streamed: true} }()
	timer := time.NewTimer(delay); hedgeStarted := false
	defer timer.Stop()
	var firstErr error
	for completed := 0; completed < 2; {
		select {
		case got := <-ch:
			completed++
			if got.err == nil {
				cancel()
				if !got.streamed && w != nil && got.result.Text != "" { _ = w.WriteDelta(got.result.Text) }
				return got.result, hedgeStarted, nil
			}
			if firstErr == nil { firstErr = got.err }
			if !hedgeStarted { return got.result, false, got.err }
		case <-timer.C:
			if !hedgeStarted { hedgeStarted = true; go func() { r, err := hedge.ChatStream(child, messages, discardWriter{}); ch <- hedgedResult{result: r, err: err, streamed: false} }() }
		case <-ctx.Done():
			return ModelResult{}, hedgeStarted, ctx.Err()
		}
	}
	return ModelResult{}, hedgeStarted, firstErr
}
