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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAccountFromKiroUser(t *testing.T) {
	email := "  Warren.Chen@Example.com "
	name := " 陈映初 "
	row := &kiroUserForConvert{
		ConnectionId: 7,
		UserId:       "6478a4a8-60a1-70d9-37bc-6aae85f6746a",
		UserEmail:    &email,
		DisplayName:  &name,
	}
	acc := accountFromKiroUser(row)
	assert.Equal(t, "kiro:KiroUser:7:6478a4a8-60a1-70d9-37bc-6aae85f6746a", acc.Id)
	// The org plugin matches accounts to users by lower-cased email, so the
	// stored email must already be normalised.
	assert.Equal(t, "warren.chen@example.com", acc.Email)
	assert.Equal(t, "陈映初", acc.FullName)
	assert.Equal(t, row.UserId, acc.UserName)
}

func TestAccountFromKiroUserWithoutIdentityStore(t *testing.T) {
	// A connection without Identity Store access has no email or display name;
	// the account still exists so a later mapping (or CSV upload) can claim it.
	row := &kiroUserForConvert{ConnectionId: 1, UserId: "u-1"}
	acc := accountFromKiroUser(row)
	assert.Equal(t, "kiro:KiroUser:1:u-1", acc.Id)
	assert.Equal(t, "", acc.Email)
	assert.Equal(t, "", acc.FullName)
	assert.Equal(t, "u-1", acc.UserName)
}

func TestConvertKiroAccountsMetaIsWiredAfterTheReportExtractor(t *testing.T) {
	assert.Equal(t, "convertKiroAccounts", ConvertKiroAccountsMeta.Name)
	assert.True(t, ConvertKiroAccountsMeta.EnabledByDefault)
	assert.Contains(t, ConvertKiroAccountsMeta.Dependencies, &ExtractKiroUserReportMeta)
	assert.Equal(t, []string{"accounts"}, ConvertKiroAccountsMeta.ProductTables)
}
