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

package models

import (
	"time"

	"github.com/apache/incubator-devlake/core/models/common"
)

// KiroSessionCommit is one session→commit edge, extracted from a trailer that
// is already sitting in the domain `commits.message` column - the code plugins
// (github, gitlab, gitextractor) collected it along with the rest of the
// message, so this table costs no extra API calls.
//
// The edge is what closes the lake's AI gap: sessions (credits, logs) join to
// commits here, commits join to pull_requests via pull_request_commits, and
// pull requests join to issues via pull_request_issues. It is an AS-IS
// association: no author check, no merged-PR requirement - those trust
// semantics live in DevDelta's three-edge attribution, not in the lake.
//
// The primary key is the edge itself. Commit messages are shared across every
// connection (domain tables are not connection-scoped), so scoping the key by
// connection or S3 slice would only duplicate rows.
type KiroSessionCommit struct {
	common.NoPKModel
	SessionId string `gorm:"primaryKey;type:varchar(64)" json:"sessionId"`
	CommitSha string `gorm:"primaryKey;type:varchar(40);index" json:"commitSha"`
	// TrailerKey records which trailer named the session: "kiro" for
	// Kiro-Session-Id, "devdelta" for Devdelta-Session-Id (other agents, e.g.
	// Claude Code). Dashboards use it to keep per-agent numbers separate.
	TrailerKey string `gorm:"type:varchar(16)" json:"trailerKey"`
}

func (KiroSessionCommit) TableName() string {
	return "_tool_kiro_session_commits"
}

// KiroCommitScanState is the incremental watermark for the trailer scan, so a
// run only re-reads commits the lake has touched since the previous run instead
// of the whole commits table every time.
type KiroCommitScanState struct {
	common.NoPKModel
	ConnectionId uint64 `gorm:"primaryKey" json:"connectionId"`
	// LastScannedUpdatedAt is the largest commits.updated_at processed so far.
	// The next run resumes from >= this value; re-reading rows that share the
	// boundary timestamp is harmless because inserts are idempotent upserts.
	LastScannedUpdatedAt *time.Time `json:"lastScannedUpdatedAt"`
}

func (KiroCommitScanState) TableName() string {
	return "_tool_kiro_commit_scan_state"
}
