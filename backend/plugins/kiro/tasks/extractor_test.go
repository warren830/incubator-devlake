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
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/apache/incubator-devlake/core/plugin"
	"github.com/apache/incubator-devlake/plugins/kiro/models"
)

// The row adapters exist so one parse produces everything a file yields; these
// verify the adaptation rather than the parsing, which is covered directly.
func TestParseUserReportRows_ReturnsBothTables(t *testing.T) {
	data := loadReportFixture(t, "02_standard_cli.csv")

	rows, err := parseUserReportRows(data, testConnectionId, testScopeId)
	require.Nil(t, err)

	var reports, modelMessages int
	for _, row := range rows {
		switch row.(type) {
		case *models.KiroUserReport:
			reports++
		case *models.KiroUserModelMessage:
			modelMessages++
		default:
			t.Fatalf("unexpected row type %T", row)
		}
	}

	// Both tables come out of a single parse because the per-model counts are
	// columns of the same CSV row - a second pass would mean reading every file
	// twice.
	assert.Equal(t, 1, reports)
	assert.Equal(t, 1, modelMessages)
}

func TestParseLogRows(t *testing.T) {
	t.Run("chat log", func(t *testing.T) {
		rows, err := parseChatLogRows(loadLogFixture(t, "chat_03_two_records.json.gz"), testConnectionId, testScopeId)
		require.Nil(t, err)
		require.Len(t, rows, 2)
		for _, row := range rows {
			_, ok := row.(*models.KiroChatLog)
			assert.True(t, ok)
		}
	})

	t.Run("completion log", func(t *testing.T) {
		rows, err := parseCompletionLogRows(loadLogFixture(t, "completion_01_non_empty.json.gz"), testConnectionId, testScopeId)
		require.Nil(t, err)
		require.Len(t, rows, 1)
		_, ok := rows[0].(*models.KiroCompletionLog)
		assert.True(t, ok)
	})

	// A record with no completions still produces a row: those records are the
	// denominator for what Kiro offered versus what was taken.
	t.Run("empty completions still produce a row", func(t *testing.T) {
		rows, err := parseCompletionLogRows(loadLogFixture(t, "completion_02_empty.json.gz"), testConnectionId, testScopeId)
		require.Nil(t, err)
		assert.Len(t, rows, 1)
	})

	t.Run("parse failure propagates", func(t *testing.T) {
		_, err := parseChatLogRows([]byte("not gzipped"), testConnectionId, testScopeId)
		assert.NotNil(t, err)
	})
}

// Twenty workers inserting into one table concurrently deadlock on MySQL gap
// locks, and DevLake's retry layer turns those deadlocks into failures that only
// appear under load - the hardest kind to reproduce. The worker closure must
// therefore stay free of database access, which is asserted structurally because
// no unit test can observe the absence of a write.
func TestExtractorWorkersDoNotTouchTheDatabase(t *testing.T) {
	source, err := os.ReadFile("extractor.go")
	require.NoError(t, err)

	body := string(source)
	workerStart := regexp.MustCompile(`scheduler\.SubmitBlocking\(func\(\) errors\.Error \{`).FindStringIndex(body)
	require.NotNil(t, workerStart, "expected a worker closure submitted to the scheduler")

	// Take the closure body up to the WaitAsync call, which is where the main
	// goroutine resumes and persistence begins.
	waitIdx := regexp.MustCompile(`scheduler\.WaitAsync\(\)`).FindStringIndex(body)
	require.NotNil(t, waitIdx)
	require.Less(t, workerStart[1], waitIdx[0])
	workerRegion := body[workerStart[1]:waitIdx[0]]

	for _, forbidden := range []string{"db.Create", "db.Update", "db.CreateOrUpdate", "db.All", "db.First", "db.Exec"} {
		assert.NotContains(t, workerRegion, forbidden,
			"worker goroutines must not access the database; %s belongs on the main goroutine", forbidden)
	}
}

// A permanently malformed object would otherwise be retried on every run,
// filling the log with the same error while the scope never reaches a finished
// state.
func TestPendingFilesQueryBoundsRetries(t *testing.T) {
	source, err := os.ReadFile("extractor.go")
	require.NoError(t, err)

	clauses := regexp.MustCompile(`dal\.Where\(\s*"([^"]+)"`).FindAllStringSubmatch(string(source), -1)
	require.NotEmpty(t, clauses)

	var found bool
	for _, clause := range clauses {
		if regexp.MustCompile(`attempt_count\s*<`).MatchString(clause[1]) {
			found = true
			// The same query must also scope to the connection and scope, or one
			// scope's run would pick up another's files.
			assert.Contains(t, clause[1], "connection_id")
			assert.Contains(t, clause[1], "scope_id")
			assert.Contains(t, clause[1], "file_type")
		}
	}
	assert.True(t, found, "the pending-files query must bound attempt_count")
}

func TestMaxAttemptsIsBounded(t *testing.T) {
	// A cap that is zero or negative would stop all extraction; an unbounded one
	// would never converge.
	assert.Greater(t, models.MaxAttempts, 0)
	assert.LessOrEqual(t, models.MaxAttempts, 10)
}

// Every extractor must declare the collector as a dependency: without the file
// metadata rows there is nothing to extract, and DevLake would otherwise be free
// to schedule extraction before discovery.
func TestExtractorSubTaskMetas(t *testing.T) {
	for _, meta := range []plugin.SubTaskMeta{
		ExtractKiroUserReportMeta,
		ExtractKiroChatLogMeta,
		ExtractKiroCompletionLogMeta,
	} {
		t.Run(meta.Name, func(t *testing.T) {
			assert.NotEmpty(t, meta.Name)
			assert.NotNil(t, meta.EntryPoint)
			assert.True(t, meta.EnabledByDefault)

			var dependsOnCollector bool
			for _, dep := range meta.Dependencies {
				if dep.Name == CollectKiroS3FilesMeta.Name {
					dependsOnCollector = true
				}
			}
			assert.True(t, dependsOnCollector, "extraction depends on file discovery having run")
		})
	}
}
