package hcsshim

import (
	"fmt"
	"os"
	"path/filepath"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
)

const (
	// HCSOPTION_ constants are string values which can be added in the RuntimeOptions of a call to CreateContainerEx.
	//HCSOPTION_ADDITIONAL_JSON_V1 = "hcs.additional.v1.json" // HCS:  Additional JSON to merge into Create calls in HCS for V1 schema. Default is none
	//HCSOPTION_ADDITIONAL_JSON_V2 = "hcs.additional.v2.json" // HCS:  Additional JSON to merge into Create calls in HCS for V2.x schema. Default is none
	//HCSOPTION_WCOW_V2_UVM_MEMORY_OVERHEAD = "hcs.wcow.v2.uvm.additional.memory" // WCOW: v2 schema MB of memory to add to WCOW UVM when calculating resources. Defaults to 256MB
	//HCSOPTION_LCOW_GLOBALMODE     = "lcow.globalmode"     // LCOW: Utility VM lifetime. Presence of this causes global mode which is insecure, but more efficient. Default is non-global
	//HCSOPTION_LCOW_SANDBOXSIZE_GB = "lcow.sandboxsize.gb" // LCOW: Size of sandbox in GB
	//HCSOPTION_LCOW_TIMEOUT = "lcow.timeout" // LCOW: Timeout (seconds) waiting for utility VM operations to complete.

	// WINDOWS_BUILD_ constants are hopefully self explanatory :) RS2 was a client-only release in case you're asking why it's not in the list.
	WINDOWS_BUILD_RS1 = 14393
	WINDOWS_BUILD_RS3 = 16299
	WINDOWS_BUILD_RS4 = 17134
	WINDOWS_BUILD_RS5 = 17659 // TODO Bump to final RS5 build
)

// CreateOptions are the set of fields used to call CreateContainerEx().
// Note: In the spec, the LayerFolders must be arranged in the same way in which
// moby configures them: layern, layern-1,...,layer2,layer1,sandbox
// where layer1 is the base read-only layer, layern is the top-most read-only
// layer, and sandbox is the RW layer. This is for historical reasons only.
type CreateOptions struct {

	// Common parameters
	Id              string         // Identifier for the container
	Owner           string         // Specifies the owner. Defaults to executable name.
	Spec            *specs.Spec    // Definition of the container or utility VM being created
	SchemaVersion   *SchemaVersion // Requested Schema Version. Defaults to v2 for RS5, v1 for RS1..RS4
	HostingSystem   Container      // Container object representing a utility or service VM in which the container is to be created.
	AsHostingSystem bool           // This is a utility VM for hosting containers, or for use as a service VM

	// LCOW specific parameters
	KirdPath          string // Folder in which kernel and initrd reside. Defaults to \Program Files\Linux Containers
	KernelFile        string // Filename under KirdPath for the kernel. Defaults to bootx64.efi
	InitrdFile        string // Filename under KirdPath for the initrd image. Defaults to initrd.img
	KernelBootOptions string // Additional boot options for the kernel

	// Internal fields
	actualSchemaVersion *SchemaVersion // Calculated based on Windows build and optional caller-supplied override
	actualId            string         // Identifier for the container
	actualOwner         string         // Owner for the container
	actualKirdPath      string         // LCOW kernel/initrd path
	actualKernelFile    string         // LCOW kernel file
	actualInitrdFile    string         // LCOW initrd file
}

// CreateContainerEx creates a container. It can cope with a  wide variety of
// scenarios, including v1 HCS schema calls, as well as more complex v2 HCS schema
// calls.
func CreateContainerEx(createOptions *CreateOptions) (Container, error) {
	logrus.Debugf("hcsshim::CreateContainerEx options: %+v", createOptions)

	createOptions.actualId = createOptions.Id
	if createOptions.actualId == "" {
		g, err := GenerateGUID()
		if err != nil {
			return nil, fmt.Errorf("failed to generate GUID for container ID: %s", err)
		}
		createOptions.actualId = g.ToString()
	}
	createOptions.actualOwner = createOptions.Owner
	if createOptions.actualOwner == "" {
		createOptions.actualOwner = filepath.Base(os.Args[0])
	}

	if createOptions.Spec == nil {
		return nil, fmt.Errorf("Spec must be supplied")
	}

	if createOptions.HostingSystem != nil {
		// By definition, a hosting system can only be supplied for a v2 Xenon.
		if !createOptions.HostingSystem.SchemaVersion().IsV20() {
			return nil, fmt.Errorf("supplied hosting system must be a v2 schema container")
		}
		createOptions.actualSchemaVersion = createOptions.HostingSystem.SchemaVersion()
	} else {
		createOptions.actualSchemaVersion = determineSchemaVersion(createOptions.SchemaVersion)
		logrus.Debugf("hcsshim::CreateContainerEx using schema %s", createOptions.actualSchemaVersion.String())
	}

	if createOptions.Spec.Linux != nil {
		if createOptions.Spec.Windows == nil {
			return nil, fmt.Errorf("containerSpec 'Windows' field must container layer folders for a Linux container")
		}
		getLCOWSettings(createOptions)
		if createOptions.actualSchemaVersion.IsV10() {
			logrus.Debugln("hcsshim::CreateContainerEx Calling createLCOWv1")
			return createLCOWv1(createOptions)
		} else {
			// TODO v2 LCOW
			panic("LCOW v2 not implemented")
		}
	}

	//
	// Is a WCOW request.
	//

	// Is it a Utility VM?
	if createOptions.AsHostingSystem {
		return createWCOWv2UVM(createOptions)
	}

	// So it's a container.
	return createWCOWContainer(createOptions)
}