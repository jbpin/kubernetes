/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stype "k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/api/v1"
	"k8s.io/kubernetes/pkg/client/clientset_generated/clientset"
	"k8s.io/kubernetes/pkg/cloudprovider/providers/vsphere"
	"k8s.io/kubernetes/test/e2e/framework"
)

/*
	Test to verify fstype specified in storage-class is being honored after volume creation.

	Steps
	1. Create StorageClass with fstype set to valid type.
	2. Create PVC which uses the StorageClass created in step 1.
	3. Wait for PV to be provisioned.
	4. Wait for PVC's status to become Bound.
	5. Create pod using PVC on specific node.
	6. Wait for Disk to be attached to the node.
	7. Execute command in the pod to get fstype.
	8. Delete pod and Wait for Volume Disk to be detached from the Node.
	9. Delete PVC, PV and Storage Class.
*/

var _ = framework.KubeDescribe("Volume fstype [Volumes]", func() {
	f := framework.NewDefaultFramework("volume-fstype")
	var (
		client    clientset.Interface
		namespace string
		nodeName  string
	)
	BeforeEach(func() {
		framework.SkipUnlessProviderIs("vsphere")
		client = f.ClientSet
		namespace = f.Namespace.Name
		nodeList := framework.GetReadySchedulableNodesOrDie(f.ClientSet)
		if len(nodeList.Items) != 0 {
			nodeName = nodeList.Items[0].Name
		} else {
			framework.Failf("Unable to find ready and schedulable Node")
		}
	})

	It("verify fstype - ext3 formatted volume", func() {
		By("Invoking Test for fstype: ext3")
		invokeTestForFstype(client, namespace, "ext3", "ext3")
	})

	It("verify disk format type - default value should be ext4", func() {
		By("Invoking Test for fstype: Default Value")
		invokeTestForFstype(client, namespace, "", "ext4")
	})
})

func invokeTestForFstype(client clientset.Interface, namespace string, fstype string, expectedContent string) {

	framework.Logf("Invoking Test for fstype: %s", fstype)
	scParameters := make(map[string]string)
	scParameters["fstype"] = fstype

	By("Creating Storage Class With Fstype")
	storageClassSpec := getVSphereStorageClassSpec("fstype", scParameters)
	storageclass, err := client.StorageV1beta1().StorageClasses().Create(storageClassSpec)
	Expect(err).NotTo(HaveOccurred())

	defer client.StorageV1beta1().StorageClasses().Delete(storageclass.Name, nil)

	By("Creating PVC using the Storage Class")
	pvclaimSpec := getVSphereClaimSpecWithStorageClassAnnotation(namespace, storageclass)
	pvclaim, err := client.CoreV1().PersistentVolumeClaims(namespace).Create(pvclaimSpec)
	Expect(err).NotTo(HaveOccurred())

	defer func() {
		client.CoreV1().PersistentVolumeClaims(namespace).Delete(pvclaimSpec.Name, nil)
	}()

	By("Waiting for claim to be in bound phase")
	err = framework.WaitForPersistentVolumeClaimPhase(v1.ClaimBound, client, pvclaim.Namespace, pvclaim.Name, framework.Poll, framework.ClaimProvisionTimeout)
	Expect(err).NotTo(HaveOccurred())

	// Get new copy of the claim
	pvclaim, err = client.CoreV1().PersistentVolumeClaims(pvclaim.Namespace).Get(pvclaim.Name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	// Get the bound PV
	pv, err := client.CoreV1().PersistentVolumes().Get(pvclaim.Spec.VolumeName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	By("Creating pod to attach PV to the node")
	// Create pod to attach Volume to Node
	podSpec := getVSpherePodSpecWithClaim(pvclaim.Name, nil, "/bin/df -T /mnt/test | /bin/awk 'FNR == 2 {print $2}' > /mnt/test/fstype && while true ; do sleep 2 ; done")
	pod, err := client.CoreV1().Pods(namespace).Create(podSpec)
	Expect(err).NotTo(HaveOccurred())

	By("Waiting for pod to be running")
	Expect(framework.WaitForPodNameRunningInNamespace(client, pod.Name, namespace)).To(Succeed())

	pod, err = client.CoreV1().Pods(namespace).Get(pod.Name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	vsp, err := vsphere.GetVSphere()
	Expect(err).NotTo(HaveOccurred())
	verifyVSphereDiskAttached(vsp, pv.Spec.VsphereVolume.VolumePath, k8stype.NodeName(pod.Spec.NodeName))

	_, err = framework.LookForStringInPodExec(namespace, pod.Name, []string{"/bin/cat", "/mnt/test/fstype"}, expectedContent, time.Minute)
	By("Delete pod and wait for volume to be detached from node")
	deletePodAndWaitForVolumeToDetach(client, namespace, vsp, pod.Spec.NodeName, pod, pv.Spec.VsphereVolume.VolumePath)

}
