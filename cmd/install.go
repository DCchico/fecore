package cmd

import (
	"fmt"
	"io"
	"os"
	"path"

	systemd "github.com/faasedge/core/pkg/systemd"
	"github.com/pkg/errors"

	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install core",
	RunE:  runInstall,
}

const workingDirectoryPermission = 0644

const corewd = "/var/lib/core"

const coreProviderWd = "/var/lib/core-provider"

func runInstall(_ *cobra.Command, _ []string) error {

	if err := ensureWorkingDir(path.Join(corewd, "secrets")); err != nil {
		return err
	}

	if err := ensureWorkingDir(coreProviderWd); err != nil {
		return err
	}

	if basicAuthErr := makeBasicAuthFiles(path.Join(corewd, "secrets")); basicAuthErr != nil {
		return errors.Wrap(basicAuthErr, "cannot create basic-auth-* files")
	}

	if err := cp("docker-compose.yaml", corewd); err != nil {
		return err
	}

	if err := cp("prometheus.yml", corewd); err != nil {
		return err
	}

	if err := cp("resolv.conf", corewd); err != nil {
		return err
	}

	err := binExists("/usr/local/bin/", "core")
	if err != nil {
		return err
	}

	err = systemd.InstallUnit("core-provider", map[string]string{
		"Cwd":             coreProviderWd,
		"SecretMountPath": path.Join(corewd, "secrets")})

	if err != nil {
		return err
	}

	err = systemd.InstallUnit("core", map[string]string{"Cwd": corewd})
	if err != nil {
		return err
	}

	err = systemd.DaemonReload()
	if err != nil {
		return err
	}

	err = systemd.Enable("core-provider")
	if err != nil {
		return err
	}

	err = systemd.Enable("core")
	if err != nil {
		return err
	}

	err = systemd.Start("core-provider")
	if err != nil {
		return err
	}

	err = systemd.Start("core")
	if err != nil {
		return err
	}

	fmt.Println(`Check status with:
  sudo journalctl -u core --lines 100 -f

Login with:
  sudo cat /var/lib/core/secrets/basic-auth-password | faas-cli login -s`)

	return nil
}

func binExists(folder, name string) error {
	findPath := path.Join(folder, name)
	if _, err := os.Stat(findPath); err != nil {
		return fmt.Errorf("unable to stat %s, install this binary before continuing", findPath)
	}
	return nil
}
func ensureSecretsDir(folder string) error {
	if _, err := os.Stat(folder); err != nil {
		err = os.MkdirAll(folder, secretDirPermission)
		if err != nil {
			return err
		}
	}

	return nil
}
func ensureWorkingDir(folder string) error {
	if _, err := os.Stat(folder); err != nil {
		err = os.MkdirAll(folder, workingDirectoryPermission)
		if err != nil {
			return err
		}
	}

	return nil
}

func cp(source, destFolder string) error {
	file, err := os.Open(source)
	if err != nil {
		return err

	}
	defer file.Close()

	out, err := os.Create(path.Join(destFolder, source))
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, file)

	return err
}
