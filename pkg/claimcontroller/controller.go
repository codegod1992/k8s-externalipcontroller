// Copyright 2016 Mirantis
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package claimcontroller

import (
	"fmt"
	"time"

	"github.com/Mirantis/k8s-externalipcontroller/pkg/extensions"
	"github.com/Mirantis/k8s-externalipcontroller/pkg/netutils"
	"github.com/Mirantis/k8s-externalipcontroller/pkg/workqueue"

	"github.com/golang/glog"
	"k8s.io/client-go/1.5/kubernetes"
	"k8s.io/client-go/1.5/pkg/api"
	"k8s.io/client-go/1.5/pkg/api/errors"
	"k8s.io/client-go/1.5/pkg/runtime"
	"k8s.io/client-go/1.5/pkg/watch"
	"k8s.io/client-go/1.5/rest"
	"k8s.io/client-go/1.5/tools/cache"
)

func NewClaimController(iface, uid string, config *rest.Config) (*claimController, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	ext, err := extensions.WrapClientsetWithExtensions(clientset, config)
	if err != nil {
		return nil, err
	}
	claimSource := &cache.ListWatch{
		ListFunc: func(options api.ListOptions) (runtime.Object, error) {
			return ext.IPClaims().List(options)
		},
		WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
			return ext.IPClaims().Watch(options)
		},
	}
	queue := workqueue.NewQueue()
	return &claimController{
		Clientset:           clientset,
		ExtensionsClientset: ext,
		Iface:               iface,
		Uid:                 uid,
		claimSource:         claimSource,
		queue:               queue,
		iphandler:           netutils.LinuxIPHandler{},
		heartbeatPeriod:     2 * time.Second,
	}, nil
}

type claimController struct {
	Clientset           *kubernetes.Clientset
	ExtensionsClientset extensions.ExtensionsClientset
	// i am not sure that it should be configurable for controller
	Iface string
	Uid   string

	claimSource cache.ListerWatcher
	claimStore  cache.Store

	queue     workqueue.QueueType
	iphandler netutils.IPHandler

	// heartbeatPeriod for a node, should be < monitorPeriod in scheduller
	heartbeatPeriod time.Duration
}

func (c *claimController) Run(stop chan struct{}) {
	go c.worker()
	go c.claimWatcher(stop)
	go c.heartbeatIpNode(stop, time.Tick(c.heartbeatPeriod))
	<-stop
	c.queue.Close()
}

func (c *claimController) claimWatcher(stop chan struct{}) {
	store, controller := cache.NewInformer(
		c.claimSource,
		&extensions.IpClaim{},
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				claim := obj.(*extensions.IpClaim)
				fmt.Printf("Received ipclaim %v -- %v\n", claim.Metadata.Name, claim.Spec.NodeName)
				if claim.Spec.NodeName != c.Uid {
					return
				}
				c.queue.Add(claim)
			},
			UpdateFunc: func(old, cur interface{}) {
				oldClaim := old.(*extensions.IpClaim)
				curClaim := cur.(*extensions.IpClaim)
				if oldClaim.Spec.NodeName != c.Uid && curClaim.Spec.NodeName != c.Uid {
					return
				}
				c.queue.Add(curClaim)
			},
			DeleteFunc: func(obj interface{}) {
				claim := obj.(*extensions.IpClaim)
				if claim.Spec.NodeName != c.Uid {
					return
				}
				c.queue.Add(claim)
			},
		},
	)
	c.claimStore = store
	controller.Run(stop)
}

func (c *claimController) worker() {
	for {
		item, quit := c.queue.Get()
		if quit {
			return
		}
		err := c.processClaim(item.(*extensions.IpClaim))
		if err != nil {
			glog.Errorf("Error processin claim %v", err)
			c.queue.Add(item)
		}
		c.queue.Done(item)
	}
}

func (c *claimController) processClaim(ipclaim *extensions.IpClaim) error {
	if _, exists, _ := c.claimStore.Get(ipclaim); !exists {
		return c.iphandler.Del(c.Iface, ipclaim.Spec.Cidr)
	}
	if ipclaim.Spec.NodeName == c.Uid {
		return c.iphandler.Add(c.Iface, ipclaim.Spec.Cidr)
	} else {
		return c.iphandler.Del(c.Iface, ipclaim.Spec.Cidr)
	}
}

func (c *claimController) heartbeatIpNode(stop chan struct{}, ticker <-chan time.Time) {
	for {
		select {
		case <-stop:
			return
		case <-ticker:
			ipnode, err := c.ExtensionsClientset.IPNodes().Get(c.Uid)
			if errors.IsNotFound(err) {
				ipnode := &extensions.IpNode{
					Metadata: api.ObjectMeta{Name: c.Uid},
				}
				_, err := c.ExtensionsClientset.IPNodes().Create(ipnode)
				if err != nil {
					glog.Errorf("Error creating node %v : %v", c.Uid, err)
				}
				continue
			}

			if err != nil {
				glog.Errorf("Error fetching node %v : %v", c.Uid, err)
				continue
			}
			_, err = c.ExtensionsClientset.IPNodes().Update(ipnode)
			if err != nil {
				glog.Errorf("Error updating node %v : %v", c.Uid, err)
				continue
			}
		}
	}
}
