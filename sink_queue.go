package discord

import (
	"context"
)

const sinkDrainBudget = 16

type sinkJob struct {
	kind     string
	step     func()
	done     chan struct{}
	lead     chan struct{}
	waiting  bool
	promoted bool
	finished bool
}

func (s *sink) submit(ctx context.Context, kind string, step func()) error {
	job := &sinkJob{kind: kind, step: step, done: make(chan struct{}), lead: make(chan struct{}, 1), waiting: true}
	s.mu.Lock()
	s.queue = append(s.queue, job)
	leads := !s.draining
	s.draining = true
	s.mu.Unlock()
	if leads && s.drain(ctx, job) {
		return s.stoppedOutcome(ctx, job)
	}
	return s.await(ctx, job)
}

func (s *sink) dispatch(ctx context.Context, kind string, step func()) {
	if err := s.submit(ctx, kind, step); err != nil {
		s.log.Debug("sink waiter gave up, its step still runs in order", "step", kind, "err", err)
	}
}

func (s *sink) await(ctx context.Context, job *sinkJob) error {
	for {
		select {
		case <-job.done:
			return nil
		case <-job.lead:
			s.mu.Lock()
			job.promoted = false
			finished := job.finished
			s.mu.Unlock()
			if finished {
				return nil
			}
			if s.drain(ctx, job) {
				return s.stoppedOutcome(ctx, job)
			}
		case <-ctx.Done():
			if s.leave(job) {
				return nil
			}
			return ctx.Err()
		}
	}
}

func (s *sink) stoppedOutcome(ctx context.Context, job *sinkJob) error {
	s.mu.Lock()
	finished := job.finished
	s.mu.Unlock()
	if finished {
		return nil
	}
	return ctx.Err()
}

func (s *sink) leave(job *sinkJob) bool {
	s.mu.Lock()
	job.waiting = false
	finished := job.finished
	pending := job.promoted && !finished
	job.promoted = false
	s.mu.Unlock()
	if pending {
		<-job.lead
		s.handOff()
	}
	return finished
}

func (s *sink) drain(ctx context.Context, own *sinkJob) bool {
	clean := false
	defer func() {
		if !clean {
			s.abort()
		}
	}()
	for range sinkDrainBudget {
		if ctx.Err() != nil {
			s.stepDown(own)
			clean = true
			return true
		}
		job := s.take()
		if job == nil {
			clean = true
			return false
		}
		s.execute(job)
	}
	s.handOff()
	clean = true
	return false
}

func (s *sink) stepDown(own *sinkJob) {
	s.mu.Lock()
	own.waiting = false
	s.mu.Unlock()
	s.handOff()
}

func (s *sink) take() *sinkJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		s.draining = false
		return nil
	}
	return s.pop()
}

func (s *sink) handOff() {
	clean := false
	defer func() {
		if !clean {
			s.abort()
		}
	}()
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.draining = false
			s.mu.Unlock()
			clean = true
			return
		}
		head := s.queue[0]
		if head.waiting {
			head.promoted = true
			head.lead <- struct{}{}
			s.mu.Unlock()
			clean = true
			return
		}
		s.pop()
		s.mu.Unlock()
		s.execute(head)
	}
}

func (s *sink) abort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range s.queue {
		s.log.Warn("sink step dropped after a panic", "step", job.kind)
		s.settleLocked(job)
	}
	s.queue = nil
	s.draining = false
}

func (s *sink) pop() *sinkJob {
	job := s.queue[0]
	s.queue[0] = nil
	s.queue = s.queue[1:]
	return job
}

func (s *sink) execute(job *sinkJob) {
	defer s.settle(job)
	if s.run().Err() != nil {
		s.log.Debug("sink step dropped, lifetime ended", "step", job.kind)
		return
	}
	if job.kind == humanKind {
		s.endTurn()
	}
	s.localize()
	job.step()
}

func (s *sink) settle(job *sinkJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settleLocked(job)
}

func (s *sink) settleLocked(job *sinkJob) {
	if job.finished {
		return
	}
	job.finished = true
	close(job.done)
}
