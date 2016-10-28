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

package workqueue

import (
	"sync"
)

type QueueType interface {
	Add(interface{})
	Get() interface{}
	Done(interface{})
	Remove(interface{})
}

func NewQueue() *Queue {
	return &Queue{
		cond:       &sync.Mutex{},
		added:      map[interface{}]bool{},
		processing: map[interface{}]bool{},
		queue:      []interface{}{},
	}
}

type Queue struct {
	cond       sync.Locker
	added      map[interface{}]bool
	processing map[interface{}]bool
	queue      []interface{}
}

func (n *Queue) Add(item interface{}) {
	n.cond.Lock()
	defer n.cond.Unlock()

	if _, exists := n.added[item]; exists {
		return
	}
	n.added[item] = true
	if _, exists := n.processing[item]; exists {
		return
	}
	n.queue = append(n.queue, item)
}

func (n *Queue) Get() (item interface{}) {
	n.cond.Lock()
	defer n.cond.Unlock()

	if len(n.queue) == 0 {
		return nil
	}

	for {
		item, n.queue = n.queue[0], n.queue[1:]
		// item was removed and shouldnt be processed
		if _, exists := n.added[item]; !exists {
			continue
		}
		break
	}
	n.processing[item] = true
	delete(n.added, item)
	return item
}

func (n *Queue) Done(item interface{}) {
	n.cond.Lock()
	defer n.cond.Unlock()

	delete(n.processing, item)

	if _, exists := n.added[item]; exists {
		n.queue = append(n.queue, item)
	}
}

// Remove will prevent item from being processed
func (n *Queue) Remove(item interface{}) {
	n.cond.Lock()
	defer n.cond.Unlock()

	if _, exists := n.added[item]; exists {
		delete(n.added, item)
	}
}

type ProcessType interface {
	Process(func(item interface{}) error)
}

type ProcessingQueue struct {
	*Queue
}

func (p *ProcessingQueue) Process(f func(item interface{}) error) error {
	item := p.Get()
	defer p.Done(item)
	if err := f(item); err != nil {
		p.Add(item)
		return err
	}
	return nil
}
