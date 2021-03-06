/*
 * Tencent is pleased to support the open source community by making TKEStack
 * available.
 *
 * Copyright (C) 2012-2020 Tencent. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not use
 * this file except in compliance with the License. You may obtain a copy of the
 * License at
 *
 * https://opensource.org/licenses/Apache-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
 * WARRANTIES OF ANY KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations under the License.
 */

package imagetransfer

import (
	"container/list"
	"fmt"
	"strings"
	"sync"

	"tkestack.io/image-transfer/configs"
	"tkestack.io/image-transfer/pkg/apis/ccrapis"
	"tkestack.io/image-transfer/pkg/apis/tcrapis"
	"tkestack.io/image-transfer/pkg/image-transfer/options"
	"tkestack.io/image-transfer/pkg/log"
	"tkestack.io/image-transfer/pkg/transfer"
	"tkestack.io/image-transfer/pkg/utils"
)

//Client is a transfer client
type Client struct {
	// a Transfer.Job list
	jobList *list.List

	// a URLPair list
	urlPairList *list.List

	// failed list
	failedJobList         *list.List
	failedJobGenerateList *list.List

	config *configs.Configs

	// mutex
	jobListMutex               sync.Mutex
	urlPairListMutex           sync.Mutex
	failedJobListMutex         sync.Mutex
	failedJobGenerateListMutex sync.Mutex
}

// URLPair is a pair of source and target url
type URLPair struct {
	source string
	target string
}

// Run is main function of a transfer client
func (c *Client) Run() error {

	if c.config.FlagConf.Config.CCRToTCR == true {
		return c.CCRToTCRTransfer()
	}

	return c.NormalTransfer(c.config.ImageList, false)

}

//CCRToTCRTransfer transfer ccr to tcr
func (c *Client) CCRToTCRTransfer() error {

	ccrClient := ccrapis.NewCCRAPIClient()
	ccrNs, err := ccrClient.GetAllNamespaceByName(c.config.Secret, c.config.FlagConf.Config.CCRRegion)

	if err != nil {
		log.Errorf("Get ccr ns returned error: ", err)
		return err
	}

	tcrClient := tcrapis.NewTCRAPIClient()
	tcrNs, tcrID, err := tcrClient.GetAllNamespaceByName(c.config.Secret,
		c.config.FlagConf.Config.TCRRegion, c.config.FlagConf.Config.TCRName)

	if err != nil {
		log.Errorf("Get tcr ns returned error: ", err)
		return err
	}

	//create ccr ns in tcr
	failedNsList, err := c.CreateTcrNs(tcrClient, ccrNs, tcrNs, c.config.Secret, c.config.FlagConf.Config.TCRRegion, tcrID)
	if err != nil {
		log.Errorf("CreateTcrNs error: ", err)
		return err
	}

	//retry failedNsList
	if len(failedNsList) != 0 {
		log.Infof("some ccr namespace create failed in tcr, retry Create Tcr Ns.")
		for times := 0; times < c.config.FlagConf.Config.RetryNums && len(failedNsList) != 0; times++ {
			tmpFailedNsList, err := c.RetryCreateTcrNs(tcrClient, failedNsList,
				c.config.Secret, c.config.FlagConf.Config.TCRRegion)
			if err != nil {
				continue
			} else {
				failedNsList = tmpFailedNsList
			}
		}
	}

	if len(failedNsList) != 0 {
		log.Warnf("some ccr namespace create failed in tcr: ", failedNsList)
	}

	//generate transfer rules
	rulesMap, err := c.GenerateCcrToTcrRules(failedNsList, ccrClient, c.config.Secret, c.config.FlagConf.Config.CCRRegion,
		c.config.FlagConf.Config.TCRRegion, c.config.FlagConf.Config.TCRName)
	if err != nil {
		return err
	}

	return c.NormalTransfer(rulesMap, true)

}

//GenerateCcrToTcrRules generate rules of ccr transfer to tcr
func (c *Client) GenerateCcrToTcrRules(failedNsList []string, ccrClient *ccrapis.CCRAPIClient,
	secret map[string]configs.Secret, ccrRegion string, tcrRegion string, tcrName string) (map[string]string, error) {

	rulesMap, err := ccrClient.GenerateAllCcrRules(secret, ccrRegion, failedNsList, tcrRegion, tcrName)

	if err != nil {
		log.Errorf("generate ccr to tcr rules failed: ", err)
		return nil, err
	}

	return rulesMap, nil

}

//RetryCreateTcrNs retry to create tcr namespaces
func (c *Client) RetryCreateTcrNs(tcrClient *tcrapis.TCRAPIClient, retryList []string,
	secret map[string]configs.Secret, region string) ([]string, error) {
	var failedList []string

	secretID, secretKey, err := tcrapis.GetTcrSecret(secret)

	tcrNs, tcrID, err := tcrClient.GetAllNamespaceByName(c.config.Secret,
		c.config.FlagConf.Config.TCRRegion, c.config.FlagConf.Config.TCRName)

	if err != nil {
		log.Errorf("retry create tcr ns, get tcr ns error: ", err)
		return nil, err
	}

	for _, ns := range retryList {
		if !utils.IsContain(tcrNs, ns) {
			_, err := tcrClient.CreateNamespace(secretID, secretKey, region, tcrID, ns)
			if err != nil {
				log.Errorf("tcr CreateNamespace error: ", err)
				failedList = append(failedList, ns)
			}
		}
	}

	return failedList, nil

}

//CreateTcrNs create tcr namespaces
func (c *Client) CreateTcrNs(tcrClient *tcrapis.TCRAPIClient, ccrNs, tcrNs []string,
	secret map[string]configs.Secret, region string, tcrID string) ([]string, error) {

	var failedList []string

	secretID, secretKey, err := tcrapis.GetTcrSecret(secret)

	if err != nil {
		log.Errorf("GetTcrSecret error: ", err)
		return failedList, err
	}

	for _, ns := range ccrNs {
		if !utils.IsContain(tcrNs, ns) {
			_, err := tcrClient.CreateNamespace(secretID, secretKey, region, tcrID, ns)
			if err != nil {
				log.Errorf("tcr CreateNamespace error: ", err)
				failedList = append(failedList, ns)
			}
		}
	}

	return failedList, nil

}

//NormalTransfer is the normal mode of transfer
func (c *Client) NormalTransfer(imageList map[string]string, isCCRToTCR bool) error {

	for source, target := range imageList {
		// ccr to tcr will use target for map key
		if isCCRToTCR {
			c.urlPairList.PushBack(&URLPair{
				source: target,
				target: source,
			})
		} else {
			c.urlPairList.PushBack(&URLPair{
				source: source,
				target: target,
			})
		}
	}

	jobListChan := make(chan *transfer.Job, c.config.FlagConf.Config.RoutineNums)

	fmt.Println("Start to handle transfer jobs, please wait ...")

	wg := sync.WaitGroup{}

	// generate goroutines to handle transfer jobs
	wg.Add(1)

	go func() {
		defer wg.Done()
		c.jobsHandler(jobListChan)
	}()

	c.rulesHandler(jobListChan)

	wg.Wait()

	log.Infof("Start to retry failed jobs...")

	for times := 0; times < c.config.FlagConf.Config.RetryNums; times++ {
		c.Retry()
	}

	if c.failedJobList.Len() != 0 {
		log.Infof("################# %v failed transfer jobs: #################", c.failedJobList.Len())
		for e := c.failedJobList.Front(); e != nil; e = e.Next() {
			log.Infof(e.Value.(*transfer.Job).Source.GetRegistry() + "/" +
				e.Value.(*transfer.Job).Source.GetRepository() + ":" + e.Value.(*transfer.Job).Source.GetTag())

		}
	}

	if c.failedJobGenerateList.Len() != 0 {
		log.Infof("################# %v failed generate jobs: #################", c.failedJobGenerateList.Len())
		for e := c.failedJobGenerateList.Front(); e != nil; e = e.Next() {
			log.Infof(e.Value.(*URLPair).source + ": " + e.Value.(*URLPair).target)

		}
	}

	log.Infof("################# Finished, %v transfer jobs failed, %v jobs generate failed #################",
		c.failedJobList.Len(), c.failedJobGenerateList.Len())

	return nil

}

//Retry is retry the failed job
func (c *Client) Retry() {
	retryJobListChan := make(chan *transfer.Job, c.config.FlagConf.Config.RoutineNums)

	wg1 := sync.WaitGroup{}
	wg1.Add(1)
	go func() {
		defer func() {
			wg1.Done()
		}()
		c.jobsHandler(retryJobListChan)
	}()

	if c.failedJobList.Len() != 0 {
		for {
			failedJob := c.failedJobList.Front()
			if failedJob == nil {
				break
			}
			retryJobListChan <- failedJob.Value.(*transfer.Job)
			c.failedJobList.Remove(failedJob)
		}

	}

	if c.failedJobGenerateList.Len() != 0 {
		c.urlPairList.PushBackList(c.failedJobGenerateList)
		c.failedJobGenerateList.Init()
		c.rulesHandler(retryJobListChan)
	} else {
		close(retryJobListChan)
	}

	wg1.Wait()
}

// NewTransferClient creates a transfer client
func NewTransferClient(opts *options.ClientOptions) (*Client, error) {

	clientConfig, err := configs.InitConfigs(opts)

	if err != nil {
		return nil, err
	}

	return &Client{
		jobList:                    list.New(),
		urlPairList:                list.New(),
		failedJobList:              list.New(),
		failedJobGenerateList:      list.New(),
		config:                     clientConfig,
		jobListMutex:               sync.Mutex{},
		urlPairListMutex:           sync.Mutex{},
		failedJobListMutex:         sync.Mutex{},
		failedJobGenerateListMutex: sync.Mutex{},
	}, nil
}

func (c *Client) rulesHandler(jobListChan chan *transfer.Job) {
	defer func() {
		close(jobListChan)
	}()

	routineNum := c.config.FlagConf.Config.RoutineNums
	wg := sync.WaitGroup{}
	for i := 0; i < routineNum; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				urlPair, empty := c.GetURLPair()
				// no more job to generate
				if empty {
					break
				}
				moreURLPairs, err := c.GenerateTransferJob(jobListChan, urlPair.source, urlPair.target)
				if err != nil {
					log.Errorf("Generate transfer job %s to %s error: %v", urlPair.source, urlPair.target, err)
					// put to failedJobGenerateList
					c.PutAFailedURLPair(urlPair)
				}
				if moreURLPairs != nil {
					c.PutURLPairs(moreURLPairs)
				}
			}
		}()
	}
	wg.Wait()
}

func (c *Client) jobsHandler(jobListChan chan *transfer.Job) {

	routineNum := c.config.FlagConf.Config.RoutineNums
	wg := sync.WaitGroup{}
	for i := 0; i < routineNum; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				job, ok := <-jobListChan
				if !ok {
					break
				}
				if err := job.Run(); err != nil {
					c.PutAFailedJob(job)
				}
			}
		}()
	}

	wg.Wait()

}

// GetURLPair gets a URLPair from urlPairList
func (c *Client) GetURLPair() (*URLPair, bool) {
	c.urlPairListMutex.Lock()
	defer func() {
		c.urlPairListMutex.Unlock()
	}()

	urlPair := c.urlPairList.Front()
	if urlPair == nil {
		return nil, true
	}
	c.urlPairList.Remove(urlPair)

	return urlPair.Value.(*URLPair), false
}

// PutURLPairs puts a URLPair array to urlPairList
func (c *Client) PutURLPairs(urlPairs []*URLPair) {
	c.urlPairListMutex.Lock()
	defer func() {
		c.urlPairListMutex.Unlock()
	}()

	if c.urlPairList != nil {
		for _, urlPair := range urlPairs {
			c.urlPairList.PushBack(urlPair)
		}
	}

}

// GetJob return a transfer.Job struct if the job list is not empty
func (c *Client) GetJob() (*transfer.Job, bool) {
	c.jobListMutex.Lock()
	defer func() {
		c.jobListMutex.Unlock()
	}()

	job := c.jobList.Front()
	if job == nil {
		return nil, true
	}
	c.jobList.Remove(job)

	return job.Value.(*transfer.Job), false
}

// PutJob puts a transfer.Job struct to job list
func (c *Client) PutJob(job *transfer.Job) {
	c.jobListMutex.Lock()
	defer func() {
		c.jobListMutex.Unlock()
	}()

	if c.jobList != nil {
		c.jobList.PushBack(job)
	}
}

// GenerateTransferJob creates transfer jobs from source and target url,
// return URLPair array if there are more than one tags
func (c *Client) GenerateTransferJob(jobListChan chan *transfer.Job, source string, target string) ([]*URLPair, error) {
	if source == "" {
		return nil, fmt.Errorf("source url should not be empty")
	}

	sourceURL, err := utils.NewRepoURL(source)
	if err != nil {
		return nil, fmt.Errorf("url %s format error: %v", source, err)
	}

	// if dest is not specific, use default registry and namespace
	if target == "" {
		if c.config.FlagConf.Config.DefaultRegistry != "" && c.config.FlagConf.Config.DefaultNamespace != "" {
			target = c.config.FlagConf.Config.DefaultRegistry + "/" +
				c.config.FlagConf.Config.DefaultNamespace + "/" + sourceURL.GetRepoWithTag()
		} else {
			return nil, fmt.Errorf("the default registry and namespace should not be nil if you want to use them")
		}
	}

	targetURL, err := utils.NewRepoURL(target)
	if err != nil {
		return nil, fmt.Errorf("url %s format error: %v", target, err)
	}

	// multi-tags config
	tags := sourceURL.GetTag()
	if moreTag := strings.Split(tags, ","); len(moreTag) > 1 {
		if targetURL.GetTag() != "" && targetURL.GetTag() != sourceURL.GetTag() {
			return nil, fmt.Errorf("multi-tags source should not correspond to a target with tag: %s:%s",
				sourceURL.GetURL(), targetURL.GetURL())
		}

		// contains more than one tag
		var urlPairs = []*URLPair{}
		for _, t := range moreTag {
			urlPairs = append(urlPairs, &URLPair{
				source: sourceURL.GetURLWithoutTag() + ":" + t,
				target: targetURL.GetURLWithoutTag() + ":" + t,
			})
		}

		return urlPairs, nil
	}

	var imageSource *transfer.ImageSource
	var imageTarget *transfer.ImageTarget

	if security, exist := c.config.GetSecuritySpecific(sourceURL.GetRegistry(), sourceURL.GetNamespace()); exist {
		log.Infof("Find auth information for %v, username: %v", sourceURL.GetURL(), security.Username)
		imageSource, err = transfer.NewImageSource(sourceURL.GetRegistry(), sourceURL.GetRepoWithNamespace(),
			sourceURL.GetTag(), security.Username, security.Password, security.Insecure)
		if err != nil {
			return nil, fmt.Errorf("generate %s image source error: %v", sourceURL.GetURL(), err)
		}
	} else {
		log.Infof("Cannot find auth information for %v, pull actions will be anonymous", sourceURL.GetURL())
		imageSource, err = transfer.NewImageSource(sourceURL.GetRegistry(), sourceURL.GetRepoWithNamespace(),
			sourceURL.GetTag(), "", "", false)
		if err != nil {
			return nil, fmt.Errorf("generate %s image source error: %v", sourceURL.GetURL(), err)
		}
	}

	// if tag is not specific, return tags
	if sourceURL.GetTag() == "" {
		if targetURL.GetTag() != "" {
			return nil, fmt.Errorf("tag should be included both side of the config: %s:%s",
				sourceURL.GetURL(), targetURL.GetURL())
		}

		// get all tags of this source repo
		tags, err := imageSource.GetSourceRepoTags()
		if err != nil {
			return nil, fmt.Errorf("get tags failed from %s error: %v", sourceURL.GetURL(), err)
		}
		log.Infof("Get tags of %s successfully: %v", sourceURL.GetURL(), tags)

		// generate url pairs for tags
		var urlPairs = []*URLPair{}
		for _, tag := range tags {
			urlPairs = append(urlPairs, &URLPair{
				source: sourceURL.GetURL() + ":" + tag,
				target: targetURL.GetURL() + ":" + tag,
			})
		}
		return urlPairs, nil
	}

	// if source tag is set but without destinate tag, use the same tag as source
	destTag := targetURL.GetTag()
	if destTag == "" {
		destTag = sourceURL.GetTag()
	}

	if security, exist := c.config.GetSecuritySpecific(targetURL.GetRegistry(), targetURL.GetNamespace()); exist {
		log.Infof("Find auth information for %v, username: %v", targetURL.GetURL(), security.Username)
		imageTarget, err = transfer.NewImageTarget(targetURL.GetRegistry(), targetURL.GetRepoWithNamespace(),
			destTag, security.Username, security.Password, security.Insecure)
		if err != nil {
			return nil, fmt.Errorf("generate %s image target error: %v", sourceURL.GetURL(), err)
		}
	} else {
		log.Infof("Cannot find auth information for %v, push actions will be anonymous", targetURL.GetURL())
		imageTarget, err = transfer.NewImageTarget(targetURL.GetRegistry(),
			targetURL.GetRepoWithNamespace(), destTag, "", "", false)
		if err != nil {
			return nil, fmt.Errorf("generate %s image target error: %v", targetURL.GetURL(), err)
		}
	}

	jobListChan <- transfer.NewJob(imageSource, imageTarget)

	log.Infof("Generate a job for %s to %s", sourceURL.GetURL(), targetURL.GetURL())
	return nil, nil
}

// GetFailedJob gets a failed job from failedJobList
func (c *Client) GetFailedJob() (*transfer.Job, bool) {
	c.failedJobListMutex.Lock()
	defer func() {
		c.failedJobListMutex.Unlock()
	}()

	failedJob := c.failedJobList.Front()
	if failedJob == nil {
		return nil, true
	}
	c.failedJobList.Remove(failedJob)

	return failedJob.Value.(*transfer.Job), false
}

// PutAFailedJob puts a failed job to failedJobList
func (c *Client) PutAFailedJob(failedJob *transfer.Job) {

	c.failedJobListMutex.Lock()
	defer func() {
		c.failedJobListMutex.Unlock()
	}()

	if c.failedJobList != nil {
		c.failedJobList.PushBack(failedJob)
	}
}

// GetAFailedURLPair get a URLPair from failedJobGenerateList
func (c *Client) GetAFailedURLPair() (*URLPair, bool) {
	c.failedJobGenerateListMutex.Lock()
	defer func() {
		c.failedJobGenerateListMutex.Unlock()
	}()

	failedURLPair := c.failedJobGenerateList.Front()
	if failedURLPair == nil {
		return nil, true
	}
	c.failedJobGenerateList.Remove(failedURLPair)

	return failedURLPair.Value.(*URLPair), false
}

// PutAFailedURLPair puts a URLPair to failedJobGenerateList
func (c *Client) PutAFailedURLPair(failedURLPair *URLPair) {
	c.failedJobGenerateListMutex.Lock()
	defer func() {
		c.failedJobGenerateListMutex.Unlock()
	}()

	if c.failedJobGenerateList != nil {
		c.failedJobGenerateList.PushBack(failedURLPair)
	}

}
