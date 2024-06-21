package fastboot

import (
	"errors"
	"fmt"

	"github.com/google/gousb"
)

type FastbootResponseStatus string

var Status = struct {
	OKAY FastbootResponseStatus
	FAIL FastbootResponseStatus
	DATA FastbootResponseStatus
	INFO FastbootResponseStatus
}{
	OKAY: "OKAY",
	FAIL: "FAIL",
	DATA: "DATA",
	INFO: "INFO",
}

var Error = struct {
	VarNotFound    error
	DeviceNotFound error
}{
	VarNotFound:    errors.New("variable not found"),
	DeviceNotFound: errors.New("device not found"),
}

type FastbootDevice struct {
	Device  *gousb.Device
	Context *gousb.Context
	In      *gousb.InEndpoint
	Out     *gousb.OutEndpoint
	Unclaim func()
}

func FindDevices() ([]*FastbootDevice, error) {
	ctx := gousb.NewContext()
	var fastbootDevices []*FastbootDevice
	devs, err := ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		for _, cfg := range desc.Configs {
			for _, ifc := range cfg.Interfaces {
				for _, alt := range ifc.AltSettings {
					return alt.Protocol == 0x03 && alt.Class == 0xff && alt.SubClass == 0x42
				}
			}
		}
		return true
	})

	if err != nil && len(devs) == 0 {
		return nil, err
	}

	for _, dev := range devs {
		intf, done, err := dev.DefaultInterface()
		if err != nil {
			continue
		}
		inEndpoint, err := intf.InEndpoint(0x81)
		if err != nil {
			continue
		}
		outEndpoint, err := intf.OutEndpoint(0x01)
		if err != nil {
			continue
		}
		fastbootDevices = append(fastbootDevices, &FastbootDevice{
			Device:  dev,
			Context: ctx,
			In:      inEndpoint,
			Out:     outEndpoint,
			Unclaim: done,
		})
	}

	return fastbootDevices, nil
}

func FindDevice(serial string) (*FastbootDevice, error) {
	devs, err := FindDevices()

	if err != nil {
		return &FastbootDevice{}, err
	}

	for _, dev := range devs {
		s, e := dev.Device.SerialNumber()
		if e != nil {
			continue
		}
		if serial != s {
			continue
		}
		return dev, nil
	}

	return &FastbootDevice{}, Error.DeviceNotFound
}

func (d *FastbootDevice) Close() {
	d.Unclaim()
	d.Device.Close()
	d.Context.Close()
}

func (d *FastbootDevice) Send(data []byte) error {
	_, err := d.Out.Write(data)
	return err
}

func (d *FastbootDevice) GetMaxPacketSize() (int, error) {
	return d.Out.Desc.MaxPacketSize, nil
}

func (d *FastbootDevice) Recv() (FastbootResponseStatus, []byte, error) {
	var data []byte
	buf := make([]byte, d.In.Desc.MaxPacketSize)
	n, err := d.In.Read(buf)
	if err != nil {
		return Status.FAIL, []byte{}, err
	}
	data = append(data, buf[:n]...)
	var status FastbootResponseStatus
	switch string(data[:4]) {
	case "OKAY":
		status = Status.OKAY
	case "FAIL":
		status = Status.FAIL
	case "DATA":
		status = Status.DATA
	case "INFO":
		status = Status.INFO
	}
	return status, data[4:], nil
}

func (d *FastbootDevice) GetVar(variable string) (string, error) {
	err := d.Send([]byte(fmt.Sprintf("getvar:%s", variable)))
	if err != nil {
		return "", err
	}
	status, resp, err := d.Recv()
	if status == Status.FAIL {
		err = Error.VarNotFound
	}
	if err != nil {
		return "", err
	}
	return string(resp), nil
}

func (d *FastbootDevice) BootImage(data []byte) error {
	err := d.Download(data)
	if err != nil {
		return err
	}

	err = d.Send([]byte("boot"))
	if err != nil {
		return err
	}

	status, data, err := d.Recv()
	switch {
	case status != Status.OKAY:
		return fmt.Errorf("failed to boot image: %s %s", status, data)
	case err != nil:
		return err
	}
	return nil
}

func (d *FastbootDevice) Flash(partition string, data []byte) error {
	err := d.Download(data)
	if err != nil {
		return err
	}

	err = d.Send([]byte(fmt.Sprintf("flash:%s", partition)))
	if err != nil {
		return err
	}

	status, data, err := d.Recv()
	switch {
	case status != Status.OKAY:
		return fmt.Errorf("failed to flash image: %s %s", status, data)
	case err != nil:
		return err
	}

	return nil
}

func (d *FastbootDevice) Download(data []byte) error {
	data_size := len(data)
	err := d.Send([]byte(fmt.Sprintf("download:%08x", data_size)))
	if err != nil {
		return err
	}

	status, _, err := d.Recv()
	switch {
	case status != Status.DATA:
		return fmt.Errorf("failed to start data phase: %s", status)
	case err != nil:
		return err
	}

	chunk_size := 0x40040

	for i := 0; i < data_size; i += chunk_size {
		end := i + chunk_size
		if end > data_size {
			end = data_size
		}
		err := d.Send(data[i:end])
		if err != nil {
			return err
		}
	}
	status, data, err = d.Recv()
	switch {
	case status != Status.OKAY:
		return fmt.Errorf("failed to finish data phase: %s %s", status, data)
	case err != nil:
		return err
	}
	return nil
}
