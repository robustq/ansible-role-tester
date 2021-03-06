package util

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"strings"

	log "github.com/sirupsen/logrus"
)

func (dist *Distribution) AnsibleHosts(config *AnsibleConfig, report *AnsibleReport) ([]string, error) {

	// Ansible syntax check.
	if !config.Quiet {
		log.Infoln("Checking role hosts...")
	}

	args := []string{
		config.PlaybookFile,
		"--list-hosts",
	}

	out, err := AnsiblePlaybook(args, false)

	hosts := []string{}

	// Iterate over each line out output
	for _, line := range strings.Split(string(out), "\n") {
		// We're looking for something like "pattern: [u'all']"
		// This is actually stupid, but we have no alternative - yet.
		if strings.Contains(line, "pattern: [") {
			line = strings.Replace(line, "pattern: [", "", -1)
			line = strings.Replace(line, "]", "", -1)
			line = strings.TrimLeft(line, " ")
			line = strings.Replace(line, "u'", "'", -1)
			for _, host := range strings.Split(line, ",") {
				host = strings.Replace(host, "'", "", -1)
				host = strings.Trim(host, " ")
				hosts = append(hosts, host)
			}
		}
	}

	if len(hosts) == 0 {
		log.Warnf("host has been delegated to localhost")
		hosts = append(hosts, "localhost")
	}

	if len(hosts) == 0 && err != nil {
		log.Errorln(err)
		return []string{}, err
	}

	return hosts, nil
}

// IdempotenceTestRemote will run an Ansible playbook once and check the
// output for any changed or failed tasks as reported by Ansible.
func (dist *Distribution) IdempotenceTestRemote(config *AnsibleConfig) (bool, time.Duration) {

	// Test role idempotence.
	if !config.Quiet {
		log.Infoln("Testing role idempotence...")
	}

	// Adjust the playbook path.
	if strings.HasPrefix(config.PlaybookFile, "/") {
		config.PlaybookFile = fmt.Sprintf("/%v", config.PlaybookFile)
	} else {
		strings.Replace(config.PlaybookFile, config.RemotePath, "./", -1)
	}

	args := []string{
		config.PlaybookFile,
		"-i",
		dist.CID + ",",
		"-c",
		"docker",
	}

	// Add verbose if configured
	if config.Verbose {
		args = append(args, "-vvvv")
	}

	var idempotence = false
	now := time.Now()
	if !config.Quiet {
		out, _ := AnsiblePlaybook(args, true)
		idempotence = IdempotenceResult(out)
	} else {
		out, _ := AnsiblePlaybook(args, false)
		idempotence = IdempotenceResult(out)
	}

	if !config.Quiet {
		PrintIdempotenceResult(now, idempotence)
	}

	return idempotence, time.Since(now)

}

// RoleTestRemote will execute the specified playbook outside the
// container once. It will assemble a request to  pass into the
// Docker execution function DockerRun.
func (dist *Distribution) RoleTestRemote(config *AnsibleConfig) (bool, time.Duration) {

	// Test role.
	if !config.Quiet {
		log.Infoln("Running the role...")
	}

	// Adjust the playbook path.
	if strings.HasPrefix(config.PlaybookFile, "/") {
		config.PlaybookFile = fmt.Sprintf("/%v", config.PlaybookFile)
	} else {
		strings.Replace(config.PlaybookFile, config.RemotePath, "./", -1)
		//config.PlaybookFile = fmt.Sprintf("./%v", config.PlaybookFile)
	}

	args := []string{
		fmt.Sprintf("%v/%v", config.RemotePath, config.PlaybookFile),
		"-i",
		dist.CID + ",",
		"-c",
		"docker",
	}

	// Add verbose if configured
	if config.Verbose {
		args = append(args, "-vvvv")
	}

	now := time.Now()
	if !config.Quiet {
		if _, err := AnsiblePlaybook(args, true); err != nil {
			log.Errorln(err)
			return false, time.Since(now)
		}
	} else {
		if _, err := AnsiblePlaybook(args, false); err != nil {
			log.Errorln(err)
			return false, time.Since(now)
		}
	}
	if !config.Quiet {
		log.Infof("Role ran in %v", time.Since(now))
	}
	return true, time.Since(now)
}

// AnsiblePlaybook will execute a command to the ansible-playbook
// binary and use the input args as arguments for that process.
// You can request output be printed using the bool stdout.
func AnsiblePlaybook(args []string, stdout bool) (string, error) {

	// If we haven't found Ansible yet, we should look for it.
	if ansibleplaybook == "" {
		a, e := exec.LookPath("ansible-playbook")
		if e != nil {
			log.Errorln("executable 'ansible-playbook' was not found in $PATH.")
		}
		ansibleplaybook = a
	}

	// Generate the command, based on input.
	cmd := exec.Cmd{}
	cmd.Path = ansibleplaybook
	cmd.Args = []string{ansibleplaybook}

	// Add our arguments to the command.
	cmd.Args = append(cmd.Args, args...)

	// If configured, print to os.Stdout.
	if stdout {
		cmd.Stdout = os.Stdout
		cmd.Stdin = os.Stdin
		cmd.Stderr = os.Stderr
	}

	// Create a buffer for the output.
	var out bytes.Buffer
	multi := io.MultiWriter(&out)

	//if stdout && !noOutput {
	if stdout {
		multi = io.MultiWriter(&out, os.Stdout)
	}

	// Assign the output to the writer.
	cmd.Stdout = multi

	// Check the errors, return as needed.
	var wg sync.WaitGroup
	wg.Add(1)
	if err := cmd.Run(); err != nil {
		log.Errorln(err)
		return out.String(), err
	}
	wg.Done()

	// Return out output as a string.
	return out.String(), nil
}

// RoleSyntaxCheckRemote will run a syntax check of the specified container.
// This helps with pure isolation of the syntax to separate it from other
// potential Ansible versions.
func (dist *Distribution) RoleSyntaxCheckRemote(config *AnsibleConfig) bool {

	// Ansible syntax check.
	if !config.Quiet {
		log.Infoln("Checking role syntax...")
	}

	args := []string{
		config.PlaybookFile,
		"-i",
		dist.CID + ",",
		"-c",
		"docker",
		"--syntax-check",
	}

	// Add verbose if configured
	if config.Verbose {
		args = append(args, "-vvvv")
	}

	if !config.Quiet {
		_, err := AnsiblePlaybook(args, true)
		if err != nil {
			log.Errorln("Syntax check: FAIL")
			return false
		} else {
			log.Infoln("Syntax check: PASS")
			return true
		}
	} else {
		_, err := AnsiblePlaybook(args, false)
		if err != nil {
			log.Errorln(err)
			return false
		}
	}
	return true
}
