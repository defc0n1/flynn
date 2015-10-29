package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/flynn/flynn/controller/utils"
	"github.com/flynn/flynn/host/types"
	"github.com/flynn/flynn/pkg/stream"
)

type Host struct {
	ID       string
	client   utils.HostClient
	healthy  bool
	checks   int
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

func NewHost(h utils.HostClient) *Host {
	return &Host{
		ID:      h.ID(),
		client:  h,
		healthy: true,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// StreamEventsTo streams all job events from the host to the given channel in
// a goroutine, returning the current list of active jobs.
func (h *Host) StreamEventsTo(ch chan *host.Event) (map[string]host.ActiveJob, error) {
	log := logger.New("fn", "StreamEventsTo", "host.id", h.ID)
	var events chan *host.Event
	var stream stream.Stream
	connect := func() (err error) {
		log.Info("connecting job event stream")
		events = make(chan *host.Event)
		stream, err = h.client.StreamEvents("all", events)
		if err != nil {
			log.Error("error connecting job event stream", "err", err)
		}
		return
	}
	if err := connect(); err != nil {
		return nil, err
	}

	log.Info("getting active jobs")
	jobs, err := h.client.ListJobs()
	if err != nil {
		log.Error("error getting active jobs", "err", err)
		return nil, err
	}
	log.Info(fmt.Sprintf("got %d active job(s) for host %s", len(jobs), h.ID))

	go func() {
		defer stream.Close()
		defer close(h.done)
		for {
		eventLoop:
			for {
				select {
				case event, ok := <-events:
					if !ok {
						break eventLoop
					}
					ch <- event
				case <-h.stop:
					return
				}
			}

			log.Warn("job event stream disconnected", "err", stream.Err())
			// keep trying to reconnect, unless we are told to stop
		retryLoop:
			for {
				select {
				case <-h.stop:
					return
				default:
				}

				if err := connect(); err == nil {
					break retryLoop
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()
	return jobs, nil
}

func (h *Host) Close() {
	h.stopOnce.Do(func() {
		close(h.stop)
		<-h.done
	})
}
