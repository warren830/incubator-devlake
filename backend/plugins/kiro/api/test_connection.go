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

package api

import (
	"net/http"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"

	"github.com/apache/incubator-devlake/core/errors"
	"github.com/apache/incubator-devlake/core/plugin"
	"github.com/apache/incubator-devlake/helpers/pluginhelper/api"
	"github.com/apache/incubator-devlake/plugins/kiro/models"
	"github.com/apache/incubator-devlake/plugins/kiro/tasks"
)

// TestConnection validates a connection that has not been saved yet.
// @Summary test kiro connection
// @Description Test kiro connection
// @Tags plugins/kiro
// @Param body body models.KiroConn true "json body"
// @Success 200  {object} plugin.ApiResourceOutput
// @Failure 400  {object} shared.ApiBody "Bad Request"
// @Failure 500  {object} shared.ApiBody "Internal Error"
// @Router /plugins/kiro/test [POST]
func TestConnection(input *plugin.ApiResourceInput) (*plugin.ApiResourceOutput, errors.Error) {
	var connection models.KiroConnection
	if err := api.Decode(input.Body, &connection, vld); err != nil {
		return nil, err
	}
	if err := validateConnection(&connection.KiroConn); err != nil {
		return nil, errors.BadInput.Wrap(err, "connection validation failed")
	}
	if err := testConnection(&connection); err != nil {
		return nil, err
	}
	return &plugin.ApiResourceOutput{Status: http.StatusOK}, nil
}

// TestExistingConnection validates a saved connection, optionally with overrides
// from the request body.
// @Summary test existing kiro connection
// @Description Test existing kiro connection
// @Tags plugins/kiro
// @Param id path int true "connection ID"
// @Success 200  {object} plugin.ApiResourceOutput
// @Failure 400  {object} shared.ApiBody "Bad Request"
// @Failure 500  {object} shared.ApiBody "Internal Error"
// @Router /plugins/kiro/connections/{id}/test [POST]
func TestExistingConnection(input *plugin.ApiResourceInput) (*plugin.ApiResourceOutput, errors.Error) {
	connection := &models.KiroConnection{}
	if err := connectionHelper.First(connection, input.Params); err != nil {
		return nil, errors.BadInput.Wrap(err, "find connection from db")
	}
	if err := api.DecodeMapStruct(input.Body, connection, false); err != nil {
		return nil, err
	}
	if err := testConnection(connection); err != nil {
		return nil, err
	}
	return &plugin.ApiResourceOutput{Status: http.StatusOK}, nil
}

// testConnection issues a real request against every bucket in use.
//
// Constructing a client proves nothing - the AWS SDK builds one happily from
// invalid credentials, so a test that stops there reports success for a
// connection that cannot read anything. A single-key list is the cheapest call
// that actually exercises credentials and bucket permissions.
//
// Both buckets are checked when reports and logs are separated, since they may
// carry different KMS keys and IAM conditions; a connection that can read
// reports but not logs would otherwise pass and then silently collect nothing.
func testConnection(connection *models.KiroConnection) errors.Error {
	clients, err := tasks.NewKiroS3Clients(connection)
	if err != nil {
		return err
	}

	for _, bucket := range clients.Buckets() {
		client := clients.Report
		if bucket == clients.PromptLog.Bucket {
			client = clients.PromptLog
		}
		if _, listErr := client.S3.ListObjectsV2(&s3.ListObjectsV2Input{
			Bucket:  aws.String(bucket),
			MaxKeys: aws.Int64(1),
		}); listErr != nil {
			return errors.BadInput.Wrap(listErr, "cannot access s3 bucket "+bucket)
		}
	}

	return nil
}
