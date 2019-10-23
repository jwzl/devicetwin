package dtcontext

import (
	"time"
	"sync"
	"errors"
	"k8s.io/klog"
	"github.com/kubeedge/beehive/pkg/core/context"
)

type DTContext struct {
	DeviceID	string
	Context		*context.Context
	Modules		map[string]dtmodule.DTModule
	CommChan	map[string]chan interface{}
	HeartBeatChan map[string]chan interface{}
	ConfirmChan	chan interface{}
	ModuleHealth	*sync.Map	
}

func NewDTContext(c *context.Context) *DTContext {
	modules	:= make(map[string]dtmodule.DTModule)
	commChan := make(map[string]chan interface{})
	heartBeatChan:= make(map[string]chan interface{})
	confirmChan :=	make(chan interface{})
	var modulesHealth sync.Map

	return &DTContext{
		Context:	c,
		Modules:	modules,
		CommChan:	commChan,
		HeartBeatChan:	heartBeatChan,
		ConfirmChan:	confirmChan,
		ModuleHealth:	&modulesHealth,
	}
}

func (dtc *DTContext) RegisterDTModule(dtm dtmodule.DTModule){
	moduleName := dtm.Name()
	dtc.CommChan[moduleName] = make(chan interface{}, 128)
	dtc.HeartBeatChan[moduleName] = make(chan interface{}, 128)
	//Pass dtcontext to dtmodule.
	dtm.InitModule(dtc, dtc.CommChan[moduleName], dtc.HeartBeatChan[moduleName], dtc.ConfirmChan)
	dtc.Modules[moduleName] = dtm
}

func (dtc *DTContext) SendToModule(dtmName string, content interface{}) error {
	if ch, exist := dtc.CommChan[dtmName];  exist {
		ch <- content
		return nil
	}

	return errors.New("Channel not found")
}

//handle heartbeat.
func (dtc *DTContext) HandleHeartBeat(dtmName string, content string) error {
	if strings.Compare(content, "ping")	== 0 {
		dtc.ModuleHealth.Store(dtmName, time.Now().Unix())
		klog.Infof("%s is healthy %v", dtmName, time.Now().Unix())
	}else if strings.Compare(content, "stop")	== 0 {
		klog.Infof("%s stop", dtmName)
		return errors.New("stop")
	}

	return nil
}


