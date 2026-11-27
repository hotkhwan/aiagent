// internal/world/roomreviews.go
//
// A room's agent files a review page in the room (`aiagent review`), and the
// room's user checks it there. The admin sees every room's pages in the same
// list, marked with the room, and answers them the same way: the request is
// sent on to the room the page lives in, so the answer reaches that room's
// agent. This is how a request for the admin ("ready to merge, version
// bumped") comes back from a user's session.
package world

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"sync"
	"time"
)

type roomReviewCache struct {
	sync.Mutex
	at    time.Time
	list  []Review
	owner map[string]string
}

const roomReviewTTL = 5 * time.Second

// roomReviews reads every room's reviews; owner maps a review id to its room.
func (s *Service) roomReviews(ctx context.Context) ([]Review, map[string]string) {
	if s.OSUsers == nil {
		return nil, nil
	}
	s.roomRev.Lock()
	defer s.roomRev.Unlock()
	if s.roomRev.owner != nil && time.Since(s.roomRev.at) < roomReviewTTL {
		return s.roomRev.list, s.roomRev.owner
	}
	var list []Review
	owner := map[string]string{}
	// an id two rooms both list belongs to neither: a room is its user's own
	// process and could name another room's page to have its answer sent here
	claimed := map[string]int{}
	for _, name := range s.roomNames() {
		cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		status, _, b, err := s.OSUsers.do(cctx, name, "GET", apiPrefix+"/reviews", "", nil)
		cancel()
		var env struct {
			Details struct {
				Reviews []Review `json:"reviews"`
			} `json:"details"`
		}
		if err != nil || status != 200 || json.Unmarshal(b, &env) != nil {
			continue
		}
		seen := map[string]bool{}
		for _, r := range env.Details.Reviews {
			if !reviewIDRE.MatchString(r.ID) || seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			claimed[r.ID]++
			r.Owner, r.Cwd = name, ""
			owner[r.ID] = name
			list = append(list, r)
		}
	}
	kept := list[:0]
	for _, r := range list {
		if claimed[r.ID] > 1 {
			delete(owner, r.ID)
			continue
		}
		kept = append(kept, r)
	}
	for id, n := range claimed {
		if n > 1 {
			log.Printf("rooms: review %s is listed by %d rooms; routed to none", id, n)
		}
	}
	list = kept
	sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	s.roomRev.at, s.roomRev.list, s.roomRev.owner = time.Now(), list, owner
	return list, owner
}

// roomOfReview names the room a review lives in, or "" for one of this
// service's own.
func (s *Service) roomOfReview(ctx context.Context, id string) string {
	if !reviewIDRE.MatchString(id) {
		return ""
	}
	if _, err := s.Collector.review(id); err == nil {
		return ""
	}
	_, owner := s.roomReviews(ctx)
	return owner[id]
}

// forgetRoomReviews drops the cached room review lists, so the next read asks
// the rooms again (after an answer was forwarded to one of them).
func (s *Service) forgetRoomReviews() {
	s.roomRev.Lock()
	s.roomRev.owner, s.roomRev.list = nil, nil
	s.roomRev.Unlock()
}
