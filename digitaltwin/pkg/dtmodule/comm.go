package dtmodule

import (
	"time"
	"strings"
	"k8s.io/klog"
	"github.com/jwzl/wssocket/model"
	"github.com/jwzl/edgeOn/digitaltwin/pkg/types"
	"github.com/jwzl/edgeOn/digitaltwin/pkg/dtcontext"
)

type CommandFunc  func(msg interface{}) error

type CommModule struct {
	name	string
	context			*dtcontext.DTContext
	//for msg communication
	recieveChan		chan interface{}
	// for module's health check.
	heartBeatChan	chan interface{}
	confirmChan		chan interface{}
	CommandTbl 	map[string]CommandFunc
}

func NewCommModule() *CommModule {
	return &CommModule{name: types.DGTWINS_MODULE_COMM}
}

func (cm *CommModule) Name() string {
	return cm.name
}

//Init the comm module.
func (cm *CommModule) InitModule(dtc *dtcontext.DTContext, comm, heartBeat, confirm chan interface{}) {
	cm.context = dtc
	cm.recieveChan = comm
	cm.heartBeatChan = heartBeat
	cm.confirmChan = confirm
	//cm.initDeviceCommandTable()
}

//Start comm module
//TODO: Device should has a healthcheck.
func (cm *CommModule) Start(){
	//Start loop.
	for {
		select {
		case msg, ok := <-cm.recieveChan:
			if !ok {
				//channel closed.
				return
			}
			
			message, isMsgType := msg.(*model.Message )
			if isMsgType {
		 		// do handle.
				target := message.GetTarget()
				if strings.Compare("device", target) == 0 {
					//send to device.
					klog.Infof("send to device")	
					cm.sendMessageToDevice(message) 	
				}else if strings.HasPrefix(target, "cloud") {
					//send to message cloud.
					klog.Infof("send to cloud")
					cm.sendMessageToHub(message)	
				}else if strings.HasPrefix(target, "edge") {
					if strings.Compare(types.MODULE_NAME, target) == 0 {
						//this is response
						cm.dealMessageResponse(message)
					}else {
						//this is edge/app
						klog.Infof("send to edge/app")
						cm.sendMessageToHub(message)
					}
				}else{
					klog.Warningf("error message format, Ignore (%v)", message)
				}				
			}
		case v, ok := <-cm.heartBeatChan:
			if !ok {
				return
			}
			
			err := cm.context.HandleHeartBeat(cm.Name(), v.(string))
			if err != nil {
				klog.Infof("%s module stopped", cm.Name())
				return
			}
		case <-time.After(60*time.Second):
			//check  the MessageCache for response.
			cm.dealMessageTimeout()	
		}
	}
}

// sendMessageToDevice
func (cm *CommModule) sendMessageToDevice(msg *model.Message) {
	operation := msg.GetOperation()

	if strings.Compare(types.DGTWINS_OPS_RESPONSE, operation) != 0 {
		//cache this message for confirm recieve the response.
		id := msg.GetID() 
		_, exist := cm.context.MessageCache.Load(id)
		if !exist {	
			cm.context.MessageCache.Store(id, msg)
		}
	}

	//send message to protocol bus.
	cm.context.Send("bus", msg)
}

//sendMessageToHub
func (cm *CommModule) sendMessageToHub(msg *model.Message) {
	//cache this message for confirm recieve the response.
	id := msg.GetID() 
	_, exist := cm.context.MessageCache.Load(id)
	if !exist {
		cm.context.MessageCache.Store(id, msg)
	}

	//send message to message hub.
	cm.context.Send("hub", msg)
}

//dealMessageResponse
func (cm *CommModule) dealMessageResponse(msg *model.Message) {
	//If we recieve the response message, then delete cache message.
	//About the response success/failed, the corresponding resource module
	// will do these things.	   
	tag := msg.GetTag()
	_ , exist := cm.context.MessageCache.Load(tag)
	if exist {
		cm.context.MessageCache.Delete(tag) 
	}	
}

//dealMessageTimeout
func (cm *CommModule) dealMessageTimeout() {
	cm.context.MessageCache.Range(func (key interface{}, value interface{}) bool {
		msg, isMsgType := value.(*model.Message)
		if isMsgType {
			timeStamp := msg.GetTimestamp()/1e3
			now	:= time.Now().UnixNano() / 1e9
			if now - timeStamp >= types.DGTWINS_MSG_TIMEOUT {
				target := msg.GetTarget()
				operation := msg.GetOperation()
				if strings.Compare("device", target) == 0 {
					if strings.Compare(types.DGTWINS_OPS_RESPONSE, operation) != 0 {
						//mark device status is offline.
						//send package and tell twin module, device is offline.
						dgtwin := &types.DigitalTwin{
							ID: msg.GetID(),
							State:	types.DGTWINS_STATE_OFFLINE,
						}
						twins := []*types.DigitalTwin{dgtwin}
						bytes, err := types.BuildTwinMessage(types.DGTWINS_OPS_UPDATE, twins)
						if err != nil {
							return false
						}
						modelMsg := cm.context.BuildModelMessage(types.MODULE_NAME, types.MODULE_NAME, 
										types.DGTWINS_OPS_UPDATE, types.DGTWINS_RESOURCE_TWINS, bytes)
						
						cm.context.SendToModule(types.DGTWINS_MODULE_TWINS, modelMsg)
					}	
				}
				cm.context.MessageCache.Delete(key)
				return true
			}else{
				//resend this message.
				cm.context.SendToModule(types.DGTWINS_MODULE_COMM, msg)
				return true
			}
		}

		return false
	})
}
