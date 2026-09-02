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
	"fmt"
	"reflect"
	"strings"

	"github.com/apache/incubator-devlake/core/dal"
	"github.com/apache/incubator-devlake/core/errors"
	"github.com/apache/incubator-devlake/core/models/common"
	"github.com/apache/incubator-devlake/core/models/domainlayer"
	"github.com/apache/incubator-devlake/core/models/domainlayer/crossdomain"
	"github.com/apache/incubator-devlake/core/plugin"
	"github.com/apache/incubator-devlake/helpers/pluginhelper/api"
	"github.com/apache/incubator-devlake/plugins/kiro/models"
)

var _ plugin.SubTaskEntryPoint = ConvertKiroAccounts

// RawKiroUserTable labels the provenance of the accounts this plugin emits.
// Kiro has no raw layer (reports are parsed straight from S3 into tool tables),
// so the name exists only for _raw_data_table, which the batch saver uses to
// find and replace this scope's rows on the next run.
const RawKiroUserTable = "kiro_users"

// ConvertKiroAccountsMeta gives every Kiro user a row in the domain `accounts`
// table. Without it the lake knows a Kiro user only as an email inside
// _tool_kiro_user_report: the org plugin's user_accounts mapping is built from
// `accounts`, so a Kiro identity could never be attached to a person through
// the supported flow, and every person-level view of credits depended on rows
// inserted by hand. With it, connectUserAccountsExact links the account to a
// user by email (or display name) like any other plugin's account.
var ConvertKiroAccountsMeta = plugin.SubTaskMeta{
	Name:             "convertKiroAccounts",
	EntryPoint:       ConvertKiroAccounts,
	EnabledByDefault: true,
	Description:      "Convert Kiro report users into domain layer accounts",
	DomainTypes:      []string{plugin.DOMAIN_TYPE_CROSS},
	Dependencies:     []*plugin.SubTaskMeta{&ExtractKiroUserReportMeta},
	DependencyTables: []string{models.KiroUserReport{}.TableName()},
	ProductTables:    []string{crossdomain.Account{}.TableName()},
}

// kiroScopeParams is the _raw_data_params shape for rows this plugin derives
// from a scope, so scope deletion can find them.
type kiroScopeParams struct {
	ConnectionId uint64 `json:"connectionId"`
	ScopeId      string `json:"scopeId"`
}

// kiroUserForConvert is one distinct user of the scope, projected from the
// daily report rows. Email and display name are taken as MAX over the rows:
// they are constant per user in practice, and MAX picks the non-null one when
// an early row predates Identity Store enrichment.
type kiroUserForConvert struct {
	ConnectionId uint64
	UserId       string
	UserEmail    *string
	DisplayName  *string
	common.RawDataOrigin
}

// ConvertKiroAccounts emits one `accounts` row per distinct Kiro user seen in
// this scope's reports. The same user in another scope (another month) upserts
// the same row, so an account is stable across the connection's history.
func ConvertKiroAccounts(taskCtx plugin.SubTaskContext) errors.Error {
	data := taskCtx.GetData().(*KiroTaskData)
	db := taskCtx.GetDal()
	connectionId := data.Options.ConnectionId
	scopeId := data.Options.ScopeId

	cursor, err := db.Cursor(
		dal.Select("connection_id, user_id, MAX(user_email) AS user_email, MAX(display_name) AS display_name"),
		dal.From(&models.KiroUserReport{}),
		dal.Where("connection_id = ? AND scope_id = ?", connectionId, scopeId),
		dal.Groupby("connection_id, user_id"),
	)
	if err != nil {
		return errors.Default.Wrap(err, "failed to open kiro user cursor")
	}
	defer cursor.Close()

	// The converter copies the INPUT row's RawDataOrigin onto every result, and
	// the report rows carry none, so the origin is stamped on the input before
	// conversion. Filled in from the converter below once it has computed the
	// canonical table and params strings.
	var rawTable, rawParams string
	converter, err := api.NewDataConverter(api.DataConverterArgs{
		InputRowType: reflect.TypeOf(kiroUserForConvert{}),
		Input:        cursor,
		RawDataSubTaskArgs: api.RawDataSubTaskArgs{
			Ctx:    taskCtx,
			Params: kiroScopeParams{ConnectionId: connectionId, ScopeId: scopeId},
			Table:  RawKiroUserTable,
		},
		Convert: func(inputRow interface{}) ([]interface{}, errors.Error) {
			row := inputRow.(*kiroUserForConvert)
			row.RawDataOrigin = common.RawDataOrigin{RawDataParams: rawParams, RawDataTable: rawTable}
			return []interface{}{accountFromKiroUser(row)}, nil
		},
	})
	if err != nil {
		return err
	}
	rawTable, rawParams = converter.GetTable(), converter.GetParams()
	return converter.Execute()
}

// KiroAccountId is the domain id of a Kiro user: plugin, entity, connection,
// then the report's stable user id. It is spelled out rather than generated
// from a model because the user is not a tool table of its own - it is a
// projection of the report rows.
func KiroAccountId(connectionId uint64, userId string) string {
	return fmt.Sprintf("kiro:KiroUser:%d:%s", connectionId, userId)
}

// accountFromKiroUser maps one projected user to the domain account. Email is
// what the org plugin's exact matcher keys on, so it is normalised to trimmed
// lower case; the display name is kept as exported.
func accountFromKiroUser(row *kiroUserForConvert) *crossdomain.Account {
	return &crossdomain.Account{
		DomainEntity: domainlayer.DomainEntity{
			Id: KiroAccountId(row.ConnectionId, row.UserId),
		},
		Email:    strings.ToLower(strings.TrimSpace(deref(row.UserEmail))),
		FullName: strings.TrimSpace(deref(row.DisplayName)),
		UserName: row.UserId,
	}
}
