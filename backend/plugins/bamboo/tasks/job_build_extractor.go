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
	"encoding/json"
	"fmt"
	"github.com/apache/incubator-devlake/core/errors"
	"github.com/apache/incubator-devlake/core/plugin"
	helper "github.com/apache/incubator-devlake/helpers/pluginhelper/api"
	"github.com/apache/incubator-devlake/plugins/bamboo/models"
)

var _ plugin.SubTaskEntryPoint = ExtractJobBuild

func ExtractJobBuild(taskCtx plugin.SubTaskContext) errors.Error {
	rawDataSubTaskArgs, data := CreateRawDataSubTaskArgs(taskCtx, RAW_JOB_BUILD_TABLE)
	//repoMap := getRepoMap(data.Options.BambooTransformationRule.RepoMap)
	extractor, err := helper.NewApiExtractor(helper.ApiExtractorArgs{
		RawDataSubTaskArgs: *rawDataSubTaskArgs,

		Extract: func(resData *helper.RawData) ([]interface{}, errors.Error) {
			res := &models.ApiBambooPlanBuild{}
			err := errors.Convert(json.Unmarshal(resData.Data, res))
			if err != nil {
				return nil, err
			}
			plan := &SimpleJob{}
			err = errors.Convert(json.Unmarshal(resData.Input, plan))
			if err != nil {
				return nil, err
			}
			body := models.BambooJobBuild{}.Convert(res)
			body.ConnectionId = data.Options.ConnectionId
			body.ProjectKey = data.Options.ProjectKey
			body.JobKey = plan.JobKey
			body.PlanKey = plan.PlanKey
			body.PlanName = plan.PlanName
			body.PlanBuildKey = fmt.Sprintf("%s-%v", plan.PlanKey, body.Number)
			results := make([]interface{}, 0)
			results = append(results, body)
			for _, v := range res.VcsRevisions.VcsRevision {
				results = append(results, &models.BambooPlanBuildVcsRevision{
					ConnectionId:   data.Options.ConnectionId,
					PlanBuildKey:   body.PlanKey,
					RepositoryId:   v.RepositoryId,
					RepositoryName: v.RepositoryName,
					VcsRevisionKey: v.VcsRevisionKey,
				})
			}
			return results, nil
		},
	})
	if err != nil {
		return err
	}

	return extractor.Execute()
}

var ExtractJobBuildMeta = plugin.SubTaskMeta{
	Name:             "ExtractJobBuild",
	EntryPoint:       ExtractJobBuild,
	EnabledByDefault: true,
	Description:      "Extract raw data into tool layer table bamboo_plan_builds",
	DomainTypes:      []string{plugin.DOMAIN_TYPE_CICD},
}

// will be used in next pr
//func getRepoMap(rawRepoMap datatypes.JSONMap) map[int]string {
//	repoMap := make(map[int]string)
//	for k, v := range rawRepoMap {
//		if list, ok := v.([]int); ok {
//			for _, id := range list {
//				repoMap[id] = k
//			}
//		}
//	}
//	return repoMap
//}
