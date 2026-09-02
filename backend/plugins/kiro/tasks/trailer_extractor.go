/*
Licensed to the Apache Software Foundation (ASF) under one or more
contributor license agreements.  See the NOTICE file distributed with
this work for additional information regarding copyright ownership.
The ASF licenses this file to You under the Apache License, Version 2.0
(the "License"); you may not use this file except in compliance with
the License.  You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tasks

import (
	"regexp"
	"strings"
	"time"

	"github.com/apache/incubator-devlake/core/dal"
	"github.com/apache/incubator-devlake/core/errors"
	"github.com/apache/incubator-devlake/core/models/domainlayer/code"
	"github.com/apache/incubator-devlake/core/plugin"
	"github.com/apache/incubator-devlake/plugins/kiro/models"
)

var _ plugin.SubTaskEntryPoint = ExtractKiroSessionCommits

// ExtractKiroSessionCommitsMeta runs in every kiro blueprint pass. It shipped
// disabled so the first rollout could be verified from a manually created
// pipeline before it was allowed anywhere near an established collection
// blueprint; that run (2026-09-02, real lake: 68 commits scanned, 28 edges)
// passed, so it is now enabled by default. It is safe to run unattended: it
// makes no API calls, and its only failure paths are database errors.
var ExtractKiroSessionCommitsMeta = plugin.SubTaskMeta{
	Name:             "extractKiroSessionCommits",
	EntryPoint:       ExtractKiroSessionCommits,
	EnabledByDefault: true,
	Description:      "Extract session-id trailers from commit messages already in the lake into the session-commit mapping",
	DomainTypes:      []string{plugin.DOMAIN_TYPE_CROSS},
}

// sessionTrailerRe is deliberately loose, mirroring DevDelta's parser: a local
// squash indents trailers and a GitHub squash buries them mid-body, and strict
// git-trailer parsing (start-of-block only) silently loses both. Any line
// position wins; the value check below rejects false positives.
//
// Two keys are accepted. Kiro-Session-Id is what the Kiro Stop hook writes;
// Devdelta-Session-Id carries other agents (Claude Code today), so a Kiro
// dashboard never claims code another agent wrote.
var sessionTrailerRe = regexp.MustCompile(`(?m)^[ \t]*(Kiro|Devdelta)-Session-Id: *([A-Za-z0-9_-]+)`)

// sessionIDRe validates trailer values: IDE sess_<uuid> or CLI bare uuid.
var sessionIDRe = regexp.MustCompile(`^(sess_)?[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// sessionTrailer is one parsed (key, id) pair from a commit message.
type sessionTrailer struct {
	TrailerKey string // "kiro" or "devdelta"
	SessionId  string
}

// ParseSessionTrailers extracts valid session trailers from one commit
// message, deduplicated by session id in first-seen order.
func ParseSessionTrailers(message string) []sessionTrailer {
	var out []sessionTrailer
	seen := map[string]bool{}
	for _, m := range sessionTrailerRe.FindAllStringSubmatch(message, -1) {
		id := m[2]
		if !sessionIDRe.MatchString(id) || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, sessionTrailer{TrailerKey: strings.ToLower(m[1]), SessionId: id})
	}
	return out
}

// ExtractKiroSessionCommits scans domain commits for session-id trailers and
// upserts the session→commit edges.
//
// The scan is incremental: a watermark on commits.updated_at (per connection)
// means each run reads only what the code plugins have written since the last
// one, and the SQL LIKE prefilter discards the vast majority of rows before
// they ever reach Go. A malformed message can never fail the run - it simply
// yields no matches - so the only error paths are database ones.
//
// Cost: the domain commits table carries no index on updated_at, so the
// watermark bounds the rows returned but not the rows examined - every pass is
// a table scan of commits, and the subtask runs once per kiro scope in a
// blueprint. That is acceptable up to a few million commits; beyond it, an
// index on commits(updated_at) is the fix, which belongs to core rather than to
// this plugin (a plugin must not alter a domain table's schema).
func ExtractKiroSessionCommits(taskCtx plugin.SubTaskContext) errors.Error {
	data := taskCtx.GetData().(*KiroTaskData)
	db := taskCtx.GetDal()
	logger := taskCtx.GetLogger()
	connectionId := data.Options.ConnectionId

	state := &models.KiroCommitScanState{ConnectionId: connectionId}
	err := db.First(state, dal.Where("connection_id = ?", connectionId))
	if err != nil && !db.IsErrorNotFound(err) {
		return errors.Default.Wrap(err, "failed to load kiro commit scan state")
	}
	watermark := time.Time{}
	if state.LastScannedUpdatedAt != nil {
		watermark = *state.LastScannedUpdatedAt
	}

	clauses := []dal.Clause{
		dal.From(&code.Commit{}),
		// The LIKE matches both accepted keys and nothing else common; rows that
		// slip through (e.g. the string quoted inside a message body at line
		// start with a bogus value) are rejected by the value regex.
		dal.Where("message LIKE ? AND updated_at >= ?", "%-Session-Id:%", watermark),
		dal.Orderby("updated_at ASC"),
	}

	count, err := db.Count(clauses...)
	if err != nil {
		return errors.Default.Wrap(err, "failed to count trailer-bearing commits")
	}
	logger.Info("scanning %d trailer-bearing commits since %s", count, watermark.Format(time.RFC3339))
	taskCtx.SetProgress(0, int(count))

	cursor, err := db.Cursor(clauses...)
	if err != nil {
		return errors.Default.Wrap(err, "failed to open commits cursor")
	}
	defer cursor.Close()

	edges := 0
	maxSeen := watermark
	for cursor.Next() {
		commit := &code.Commit{}
		if err := db.Fetch(cursor, commit); err != nil {
			return errors.Default.Wrap(err, "failed to fetch commit row")
		}
		for _, t := range ParseSessionTrailers(commit.Message) {
			row := &models.KiroSessionCommit{
				SessionId:  t.SessionId,
				CommitSha:  commit.Sha,
				TrailerKey: t.TrailerKey,
			}
			if err := db.CreateOrUpdate(row); err != nil {
				return errors.Default.Wrap(err, "failed to upsert kiro session commit")
			}
			edges++
		}
		if commit.UpdatedAt.After(maxSeen) {
			maxSeen = commit.UpdatedAt
		}
		taskCtx.IncProgress(1)
	}

	// Persist the watermark only after the pass completes; ASC ordering means an
	// interrupted run re-reads at most this pass's rows, and upserts make that
	// re-read idempotent.
	state.LastScannedUpdatedAt = &maxSeen
	if err := db.CreateOrUpdate(state); err != nil {
		return errors.Default.Wrap(err, "failed to save kiro commit scan state")
	}

	logger.Info("extracted %d session-commit edges", edges)
	return nil
}
