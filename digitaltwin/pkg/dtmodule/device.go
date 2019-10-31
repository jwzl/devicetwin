package dtmodule

import (
	"sync"
	"errors"
	"strings"
	"k8s.io/klog"
	"encoding/json"
	"github.com/jwzl/wssocket/model"
	"github.com/jwzl/edgeOn/digitaltwin/pkg/types"
	"github.com/jwzl/edgeOn/digitaltwin/pkg/dtcontext"
)

type DeviceCommandFunc  func(msg *model.Message )(interface{}, error)			
//this module process the device Create/delete/update/query.
type DeviceModule struct {
	// module name
	name			string
	context			*dtcontext.DTContext
	//for msg communication
	recieveChan		chan interface{}
	// for module's health check.
	heartBeatChan	chan interface{}
	confirmChan		chan interface{}
	deviceCommandTbl 	map[string]DeviceCommandFunc
}

func NewDeviceModule() *DeviceModule {
	return &DeviceModule{name: types.DGTWINS_MODULE_TWINS}
}

// Device command include: create/delete, update whole device, 
// Get whole device or device list.
func (dm *DeviceModule) initDeviceCommandTable() {
	dm.deviceCommandTbl = make(map[string]DeviceCommandFunc)
	dm.deviceCommandTbl["Update"] = dm.deviceUpdateHandle
	dm.deviceCommandTbl["Delete"] = dm.deviceDeleteHandle	
	dm.deviceCommandTbl["Get"] = dm.deviceGetHandle	
}

func (dm *DeviceModule) Name() string {
	return dm.name
}

//Init the device module.
func (dm *DeviceModule) InitModule(dtc *dtcontext.DTContext, comm, heartBeat, confirm chan interface{}) {
	dm.context = dtc
	dm.recieveChan = comm
	dm.heartBeatChan = heartBeat
	dm.confirmChan = confirm
	dm.initDeviceCommandTable()
}

//Start Device module
func (dm *DeviceModule) Start(){
	//Start loop.
	for {
		select {
		case msg, ok := <-dm.recieveChan:
			if !ok {
				//channel closed.
				return
			}
			
			message, isMsgType := msg.(*model.Message )
			if isMsgType {
				klog.Infof("device module recieved message (%v)", message)
		 		// do handle.
				if fn, exist := dm.deviceCommandTbl[message.GetOperation()]; exist {
					_, err := fn(message)
					if err != nil {
						klog.Errorf("Handle %s failed, ignored", message.GetOperation())
					}
				}else {
					klog.Errorf("No this handle for %s, ignored", message.GetOperation())
				}
			}
		case v, ok := <-dm.heartBeatChan:
			if !ok {
				return
			}
			
			err := dm.context.HandleHeartBeat(dm.Name(), v.(string))
			if err != nil {
				klog.Infof("%s module stopped", dm.Name())
				return
			}
		}
	}
}

// handle device create and update.
func (dm *DeviceModule)  deviceUpdateHandle(msg *model.Message ) (interface{}, error) {
	var dgTwinMsg types.DGTwinMessage 

	msgRespWhere := msg.GetSource()
	content, ok := msg.Content.([]byte)
	if !ok {
		return nil, errors.New("invaliad message content")
	}

	err := json.Unmarshal(content, &dgTwinMsg)
	if err != nil {
		return nil, err
	}

	//get all requested twins
	for _, dgTwin := range dgTwinMsg.Twins	{
		//for each dgtwin
		deviceID := dgTwin.ID
		exist := dm.context.DGTwinIsExist(deviceID)
		if !exist {
			//Create DGTwin
			//Infutre, we will store DGTwin into sqlite database. 
			dm.context.DGTwinList.Store(deviceID, dgTwin)
			var deviceMutex	sync.Mutex
			dm.context.DGTwinMutex.Store(deviceID, &deviceMutex)
			//save to sqlite, implement in future.
			//Send Response to target.	
			msgContent, err := types.BuildResponseMessage(types.RequestSuccessCode, "Success", nil)
			if err != nil {
				//Internal err.
				return nil,  err
			}else{
				//send the msg to comm module and process it
				dm.context.SendResponseMessage(msg, msgContent)
			}	
			//notify device	
			// send broadcast to all device, and wait (own this ID) device's response,
			// if it has reply, then means that device is online.
			deviceMsg := dm.context.BuildModelMessage(types.MODULE_NAME, "device", 
						types.DGTWINS_OPS_CREATE, types.DGTWINS_RESOURCE_DEVICE, content)
			klog.Infof("Send message (%v)", deviceMsg)
			dm.context.SendToModule(types.DGTWINS_MODULE_COMM, deviceMsg)	
		}else {
			//Update DGTwin
			dm.context.Lock(deviceID)
			v, exist := dm.context.DGTwinList.Load(deviceID)
			if !exist {
				return nil, errors.New("No such dgtwin in DGTwinList")
			}
			oldTwin, isDgTwinType  :=v.(*types.DigitalTwin)
			if !isDgTwinType {
				return nil,  errors.New("invalud digital twin type")
			}

			//deal device update
			dm.dealTwinUpdate(oldTwin, dgTwin)
			dm.context.Unlock(deviceID)

			//if message's source is not edge/dgtwin, send response.
			if strings.Compare(msgRespWhere, types.MODULE_NAME) != 0 {
				msgContent, err := types.BuildResponseMessage(types.RequestSuccessCode, "Success", nil)
				if err != nil {
					//Internal err.
					return nil,  err
				}else{
					dm.context.SendResponseMessage(msg, msgContent)
				}	
			}

			//if the twin has property, let property module to do it.
			if dgTwin.Properties != nil && len(dgTwin.Properties.Desired) > 0 {
				twins := []*types.DigitalTwin{dgTwin}
				bytes, err := types.BuildTwinMessage(types.DGTWINS_OPS_UPDATE, twins)
				if err == nil {
					modelMsg := dm.context.BuildModelMessage(types.MODULE_NAME, types.MODULE_NAME, 
											types.DGTWINS_OPS_UPDATE, types.DGTWINS_MODULE_PROPERTY, bytes)
					dm.context.SendToModule(types.DGTWINS_MODULE_PROPERTY, modelMsg)
				}
			}
		}
	}
	
	return nil, nil
}

//deal twin update.
//this is a patch for the old device state.
func (dm *DeviceModule) dealTwinUpdate(oldTwin, newTwin *types.DigitalTwin) error {
	if oldTwin == nil || newTwin == nil {
		return errors.New("error oldTwin or newTwin")
	}

	klog.Infof("old twin =(%v), newTwin =(%v)", oldTwin, newTwin)
	if len(newTwin.Name) > 0 {
		oldTwin.Name = newTwin.Name
	}
	if len(newTwin.Description) > 0 {
		oldTwin.Description = newTwin.Description
	}
	if len(newTwin.State) > 0 {
		oldTwin.LastState = oldTwin.State 
		oldTwin.State = newTwin.State		
	}
	//patch all metadata to oldTwin. 
	if len(newTwin.MetaData) > 0 {
		for key, value := range newTwin.MetaData {
			oldTwin.MetaData[key] = value
		}
	}

	return nil
}

func (dm *DeviceModule)  deviceDeleteHandle(msg *model.Message) (interface{}, error) {
	var dgTwinMsg types.DGTwinMessage 

	content, ok := msg.Content.([]byte)
	if !ok {
		return nil, errors.New("invaliad message content")
	}

	err := json.Unmarshal(content, &dgTwinMsg)
	if err != nil {
		return nil, err
	}

	for _, dgTwin := range dgTwinMsg.Twins	{
		//for each dgtwin
		var msgContent  []byte
		deviceID := dgTwin.ID
		twins := []*types.DigitalTwin{dgTwin}

		exist := dm.context.DGTwinIsExist(deviceID)
		if !exist {
			msgContent, err = types.BuildResponseMessage(types.NotFoundCode, "Not found", twins)
			if err != nil {
				//Internal err.
				return nil, err
			}
		}else {
			//delete the device & mutex.
			dm.context.Lock(deviceID)
			dm.context.DGTwinList.Delete(deviceID)
			dm.context.Unlock(deviceID)
			dm.context.DGTwinMutex.Delete(deviceID)

			msgContent, err = types.BuildResponseMessage(types.RequestSuccessCode, "Deleted", twins)
			if err != nil {
				//Internal err.
				return nil, err
			}
		}
		dm.context.SendResponseMessage(msg, msgContent)

		//let device know this delete.
		dm.context.SendTwinMessage2Device(msg, types.DGTWINS_OPS_DELETE, twins)
	}

	return nil, nil
}

func (dm *DeviceModule) deviceGetHandle(msg *model.Message) (interface{}, error) {
	var dgTwinMsg types.DGTwinMessage 
	twins := make([]*types.DigitalTwin, 0)

	content, ok := msg.Content.([]byte)
	if !ok {
		return nil, errors.New("invaliad message content")
	}

	err := json.Unmarshal(content, &dgTwinMsg)
	if err != nil {
		return nil, err
	}

	for _, dgTwin := range dgTwinMsg.Twins	{
		//for each dgtwin
		deviceID := dgTwin.ID

		exist := dm.context.DGTwinIsExist(deviceID)
		if exist {
			v, _ := dm.context.DGTwinList.Load(deviceID)
			savedTwin, isDgTwinType  :=v.(*types.DigitalTwin)
			if !isDgTwinType {
				return nil,  errors.New("invalud digital twin type")
			}
			twins = append(twins, savedTwin)
		}else {
			twins := []*types.DigitalTwin{dgTwin}
			msgContent, err := types.BuildResponseMessage(types.NotFoundCode, "Not found", twins)
			if err != nil {
				//Internal err.
				return nil, err
			}
			dm.context.SendResponseMessage(msg, msgContent)
			return nil, nil
		}
	}

	//Send the response.
	msgContent, err := types.BuildResponseMessage(types.RequestSuccessCode, "Get", twins)
	if err != nil {
		//Internal err.
		return nil, err
	}
	dm.context.SendResponseMessage(msg, msgContent)

	return nil, nil
}		
