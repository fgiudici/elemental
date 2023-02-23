/*
Copyright © 2022 - 2023 SUSE LLC

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

package e2e_test

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher-sandbox/ele-testhelpers/kubectl"
	"github.com/rancher-sandbox/ele-testhelpers/tools"
	"github.com/rancher/elemental/tests/e2e/helpers/misc"
)

func checkClusterAgent(client *tools.Client) {
	// cluster-agent is the pod that communicates to Rancher, wait for it before continuing
	Eventually(func() string {
		out, _ := client.RunSSH("kubectl get pod -n cattle-system -l app=cattle-cluster-agent")
		return out
	}, misc.SetTimeout(3*time.Duration(usedNodes)*time.Minute), 10*time.Second).Should(ContainSubstring("Running"))
}

func getClusterState(ns, cluster, condition string) string {
	out, err := kubectl.Run("get", "cluster", "--namespace", ns, cluster, "-o", "jsonpath="+condition)
	Expect(err).To(Not(HaveOccurred()))
	return out
}

var _ = Describe("E2E - Bootstrapping node", Label("bootstrap"), func() {
	var (
		wg sync.WaitGroup
	)

	It("Provision the node", func() {
		// Set MachineRegistration name based on hostname
		machineRegName := "machine-registration-" + poolType + "-" + clusterName

		By("Setting emulated TPM to "+strconv.FormatBool(emulateTPM), func() {
			// Set temporary file
			emulatedTmp, err := misc.CreateTemp("emulatedTPM")
			Expect(err).To(Not(HaveOccurred()))
			defer os.Remove(emulatedTmp)

			// Save original file as it can be modified multiple time
			misc.CopyFile(emulateTPMYaml, emulatedTmp)

			// Patch the yaml file
			err = tools.Sed("emulate-tpm:.*", "emulate-tpm: "+strconv.FormatBool(emulateTPM), emulatedTmp)
			Expect(err).To(Not(HaveOccurred()))

			// And apply it
			out, err := kubectl.Run("patch", "MachineRegistration",
				"--namespace", clusterNS, machineRegName,
				"--type", "merge", "--patch-file", emulatedTmp,
			)
			Expect(err).To(Not(HaveOccurred()), out)
		})

		By("Downloading installation config file", func() {
			// Download the new YAML installation config file
			tokenURL, err := kubectl.Run("get", "MachineRegistration",
				"--namespace", clusterNS, machineRegName,
				"-o", "jsonpath={.status.registrationURL}")
			Expect(err).To(Not(HaveOccurred()))

			err = tools.GetFileFromURL(tokenURL, installConfigYaml, false)
			Expect(err).To(Not(HaveOccurred()))
		})

		if isoBoot != "true" {
			By("Configuring iPXE boot script for network installation", func() {
				numberOfFile, err := misc.ConfigureiPXE()
				Expect(err).To(Not(HaveOccurred()))
				Expect(numberOfFile).To(BeNumerically(">=", 1))
			})
		}

		if isoBoot == "true" {
			By("Adding registration file to ISO", func() {
				// Check if generated ISO is already here
				isIso, _ := exec.Command("bash", "-c", "ls ../../elemental-*.iso").Output()

				// No need to recreate the ISO twice
				if len(isIso) == 0 {
					out, err := exec.Command(
						"bash", "-c",
						"../../.github/elemental-iso-add-registration "+installConfigYaml+" ../../build/elemental-*.iso",
					).CombinedOutput()
					GinkgoWriter.Printf("%s\n", out)
					Expect(err).To(Not(HaveOccurred()))

					// Move generated ISO to the destination directory
					err = exec.Command("bash", "-c", "mv -f elemental-*.iso ../..").Run()
					Expect(err).To(Not(HaveOccurred()))
				}
			})
		}

		// Loop on node provisionning
		// NOTE: if numberOfVMs == vmIndex then only one node will be provisionned
		for index := vmIndex; index <= numberOfVMs; index++ {
			// Set node hostname
			hostName := misc.SetHostname(vmNameRoot, index)
			Expect(hostName).To(Not(BeNil()))

			// Add node in network configuration
			err := misc.AddNode(netDefaultFileName, hostName, index)
			Expect(err).To(Not(HaveOccurred()))

			// Get generated MAC address
			_, macAdrs := GetNodeInfo(hostName)
			Expect(macAdrs).To(Not(BeNil()))

			wg.Add(1)
			go func(s, h, m string) {
				defer wg.Done()
				defer GinkgoRecover()

				By("Installing node "+h, func() {
					// Execute node deployment in parallel
					err := exec.Command(s, h, m).Run()
					Expect(err).To(Not(HaveOccurred()))
				})
			}(installVMScript, hostName, macAdrs)
		}

		// Wait for all parallel jobs
		wg.Wait()
	})

	It("Add the node in Rancher Manager", func() {
		for index := vmIndex; index <= numberOfVMs; index++ {
			// Set node hostname
			hostName := misc.SetHostname(vmNameRoot, index)
			Expect(hostName).To(Not(BeNil()))

			// Execute node deployment in parallel
			wg.Add(1)
			go func(c, h string, i int) {
				defer wg.Done()
				defer GinkgoRecover()

				By("Checking that node "+h+" is available in Rancher", func() {
					Eventually(func() string {
						id, _ := misc.GetServerId(c, i)
						return id
					}, misc.SetTimeout(1*time.Minute), 5*time.Second).Should(Not(BeEmpty()))
				})
			}(clusterNS, hostName, index)
		}

		// Wait for all parallel jobs
		wg.Wait()

		if vmIndex > 1 {
			By("Checking cluster state", func() {
				CheckClusterState(clusterNS, clusterName)
			})
		}

		By("Incrementing number of nodes in "+poolType+" pool", func() {
			// Increase 'quantity' field
			value, err := misc.IncreaseQuantity(clusterNS,
				clusterName,
				"pool-"+poolType+"-"+clusterName, usedNodes)
			Expect(err).To(Not(HaveOccurred()))
			Expect(value).To(BeNumerically(">=", 1))

			// Check that the selector has been correctly created
			Eventually(func() string {
				out, _ := kubectl.Run("get", "MachineInventorySelector",
					"--namespace", clusterNS,
					"-o", "jsonpath={.items[*].metadata.name}")
				return out
			}, misc.SetTimeout(3*time.Minute), 5*time.Second).Should(ContainSubstring("selector-" + poolType + "-" + clusterName))
		})

		By("Waiting for known cluster state before adding the node(s)", func() {
			msg := `(configuring .* node\(s\)|waiting for viable init node)`
			Eventually(func() string {
				clusterMsg := getClusterState(clusterNS, clusterName,
					"{.status.conditions[?(@.type==\"Updated\")].message}")

				if clusterMsg == "" {
					clusterMsg = getClusterState(clusterNS, clusterName,
						"{.status.conditions[?(@.type==\"Provisioned\")].message}")
				}

				return clusterMsg
			}, misc.SetTimeout(5*time.Duration(usedNodes)*time.Minute), 10*time.Second).Should(MatchRegexp(msg))
		})

		for index := vmIndex; index <= numberOfVMs; index++ {
			// Set node hostname
			hostName := misc.SetHostname(vmNameRoot, index)
			Expect(hostName).To(Not(BeNil()))

			// Get node information
			client, _ := GetNodeInfo(hostName)
			Expect(client).To(Not(BeNil()))

			// Execute in parallel
			wg.Add(1)
			go func(c, h string, i int, t bool, cl *tools.Client) {
				defer wg.Done()
				defer GinkgoRecover()

				// Restart the node(s)
				By("Restarting "+h+" to add it in the cluster", func() {
					err := exec.Command("sudo", "virsh", "start", h).Run()
					Expect(err).To(Not(HaveOccurred()))
				})

				By("Checking "+h+" SSH connection", func() {
					// Retry the SSH connection, as it can takes time for the user to be created
					Eventually(func() string {
						out, _ := cl.RunSSH("echo SSH_OK")
						out = strings.Trim(out, "\n")
						return out
					}, misc.SetTimeout(10*time.Minute), 5*time.Second).Should(Equal("SSH_OK"))
				})

				By("Checking that TPM is correctly configured on "+h, func() {
					testValue := "-c"
					if t == true {
						testValue = "! -e"
					}
					Eventually(func() error {
						_, err := cl.RunSSH("[[ " + testValue + " /dev/tpm0 ]]")
						return err
					}, misc.SetTimeout(1*time.Minute), 5*time.Second).Should(Not(HaveOccurred()))
				})

				By("Checking OS version on "+h, func() {
					out, err := cl.RunSSH("cat /etc/os-release")
					Expect(err).To(Not(HaveOccurred()))
					GinkgoWriter.Printf("OS Version on %s:\n%s\n", h, out)
				})
			}(clusterNS, hostName, index, emulateTPM, client)
		}

		// Wait for all parallel jobs
		wg.Wait()

		if poolType != "worker" {
			for index := vmIndex; index <= numberOfVMs; index++ {
				// Set node hostname
				hostName := misc.SetHostname(vmNameRoot, index)
				Expect(hostName).To(Not(BeNil()))

				// Get node information
				client, _ := GetNodeInfo(hostName)
				Expect(client).To(Not(BeNil()))

				// Execute in parallel
				wg.Add(1)
				go func(h string, cl *tools.Client) {
					defer wg.Done()
					defer GinkgoRecover()

					if strings.Contains(k8sVersion, "rke2") {
						By("Configuring kubectl command on node "+h, func() {
							dir := "/var/lib/rancher/rke2/bin"
							kubeCfg := "export KUBECONFIG=/etc/rancher/rke2/rke2.yaml"

							// Wait a little to be sure that RKE2 installation has started
							// Otherwise the directory is not available!
							Eventually(func() error {
								_, err := cl.RunSSH("[[ -d " + dir + " ]]")
								return err
							}, misc.SetTimeout(3*time.Minute), 5*time.Second).Should(Not(HaveOccurred()))

							// Configure kubectl
							_, err := cl.RunSSH("I=" + dir + "/kubectl; if [[ -x ${I} ]]; then ln -s ${I} bin/; echo " + kubeCfg + " >> .bashrc; fi")
							Expect(err).To(Not(HaveOccurred()))
						})
					}

					By("Checking kubectl command on "+h, func() {
						// Check if kubectl works
						Eventually(func() string {
							out, _ := cl.RunSSH("kubectl version 2>/dev/null | grep 'Server Version:'")
							return out
						}, misc.SetTimeout(5*time.Minute), 5*time.Second).Should(ContainSubstring(k8sVersion))
					})

					By("Checking cluster agent on "+h, func() {
						checkClusterAgent(cl)
					})
				}(hostName, client)
			}

			// Wait for all parallel jobs
			wg.Wait()
		}

		By("Checking cluster state", func() {
			CheckClusterState(clusterNS, clusterName)
		})

		if poolType != "worker" {
			for index := vmIndex; index <= numberOfVMs; index++ {
				// Set node hostname
				hostName := misc.SetHostname(vmNameRoot, index)
				Expect(hostName).To(Not(BeNil()))

				// Get node information
				client, _ := GetNodeInfo(hostName)
				Expect(client).To(Not(BeNil()))

				// Execute in parallel
				wg.Add(1)
				go func(h string, cl *tools.Client) {
					defer wg.Done()
					defer GinkgoRecover()

					By("Checking cluster version on "+h, func() {
						// Show cluster version, could be useful for debugging purposes
						version, err := client.RunSSH("kubectl version")
						Expect(err).To(Not(HaveOccurred()))
						GinkgoWriter.Printf("K8s version on %s:\n%s\n", h, version)
					})
				}(hostName, client)
			}

			// Wait for all parallel jobs
			wg.Wait()
		}

		for index := vmIndex; index <= numberOfVMs; index++ {
			// Set node hostname
			hostName := misc.SetHostname(vmNameRoot, index)
			Expect(hostName).To(Not(BeNil()))

			// Get node information
			client, _ := GetNodeInfo(hostName)
			Expect(client).To(Not(BeNil()))

			// Execute in parallel
			wg.Add(1)
			go func(h, p string, cl *tools.Client) {
				defer wg.Done()
				defer GinkgoRecover()

				By("Rebooting "+h, func() {
					// Execute 'reboot' in background, to avoid SSH locking
					_, err := cl.RunSSH("setsid -f reboot")
					Expect(err).To(Not(HaveOccurred()))

					// Wait a little bit for the cluster to be in an unstable state (yes!)
					time.Sleep(misc.SetTimeout(2 * time.Minute))
				})

				if p != "worker" {
					By("Checking cluster agent on "+h, func() {
						checkClusterAgent(cl)
					})
				}
			}(hostName, poolType, client)
		}

		// Wait for all parallel jobs
		wg.Wait()

		By("Checking cluster state after reboot", func() {
			CheckClusterState(clusterNS, clusterName)
		})
	})
})
