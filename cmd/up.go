package cmd

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/sethvargo/go-password/password"
	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"

	"bytes"

	units "github.com/docker/go-units"
	"github.com/go-redis/redis/v8"
	"github.gatech.edu/faasedge/fecore/pkg"
)

// upConfig are the CLI flags used by the `fecore up` command to deploy the fecore service
type upConfig struct {
	// composeFilePath is the path to the compose file specifying the fecore service configuration
	// See https://compose-spec.io/ for more information about the spec,
	//
	// currently, this must be the name of a file in workingDir, which is set to the value of
	// `fecorewd = /var/lib/fecore`
	composeFilePath string

	// working directory to assume the compose file is in, should be fecorewd.
	// this is not configurable but may be in the future.
	workingDir string
}

func init() {
	configureUpFlags(upCmd.Flags())
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start fecore",
	RunE:  runUp,
}

func runUp(cmd *cobra.Command, _ []string) error {

	printVersion()

	cfg, err := parseUpFlags(cmd)
	if err != nil {
		return err
	}

	services, err := loadServiceDefinition(cfg)
	if err != nil {
		return err
	}

	basicAuthErr := makeBasicAuthFiles(path.Join(cfg.workingDir, "secrets"))
	if basicAuthErr != nil {
		return errors.Wrap(basicAuthErr, "cannot create basic-auth-* files")
	}

	start := time.Now()
	supervisor, err := pkg.NewSupervisor("/run/containerd/containerd.sock")
	if err != nil {
		return err
	}

	log.Printf("Supervisor created in: %s\n", units.HumanDuration(time.Since(start)))

	start = time.Now()
	if err := supervisor.Start(services); err != nil {
		return err
	}
	defer supervisor.Close()

	log.Printf("Supervisor init done in: %s\n", units.HumanDuration(time.Since(start)))

	shutdownTimeout := time.Second * 1
	timeout := time.Second * 60

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

		log.Printf("fecore: waiting for SIGTERM or SIGINT\n")
		<-sig

		log.Printf("Signal received.. shutting down server in %s\n", shutdownTimeout.String())
		err := supervisor.Remove(services)
		if err != nil {
			fmt.Println(err)
		}

		// TODO: close proxies
		time.AfterFunc(shutdownTimeout, func() {
			wg.Done()
		})
	}()

	localResolver := pkg.NewLocalResolver(path.Join(cfg.workingDir, "hosts"))
	go localResolver.Start()

	proxies := map[uint32]*pkg.Proxy{}
	for _, svc := range services {
		for _, port := range svc.Ports {

			listenPort := port.Port
			if _, ok := proxies[listenPort]; ok {
				return fmt.Errorf("port %d already allocated", listenPort)
			}

			hostIP := "0.0.0.0"
			if len(port.HostIP) > 0 {
				hostIP = port.HostIP
			}

			upstream := fmt.Sprintf("%s:%d", svc.Name, port.TargetPort)
			proxies[listenPort] = pkg.NewProxy(upstream, listenPort, hostIP, timeout, localResolver)
		}
	}

	// TODO: track proxies for later cancellation when receiving sigint/term
	for _, v := range proxies {
		go v.Start()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	redisClient, err := startRedis(ctx)
	if err != nil {
		return err
	}
	defer func() {
		redisClient.Close()
		cancel() // This will stop the Redis server
	}()

	// Test Redis connection
	err = redisClient.Set(ctx, "test_key", "test_value", 0).Err()
	if err != nil {
		log.Printf("Failed to set key in Redis: %v", err)
	} else {
		value, err := redisClient.Get(ctx, "test_key").Result()
		if err != nil {
			log.Printf("Failed to get key from Redis: %v", err)
		} else {
			log.Printf("Redis test successful. Key: test_key, Value: %s", value)
		}
	}

	wg.Wait()
	return nil
}

func makeBasicAuthFiles(wd string) error {

	pwdFile := path.Join(wd, "basic-auth-password")
	authPassword, err := password.Generate(63, 10, 0, false, true)

	if err != nil {
		return err
	}

	err = makeFile(pwdFile, authPassword)
	if err != nil {
		return err
	}

	userFile := path.Join(wd, "basic-auth-user")
	err = makeFile(userFile, "admin")
	if err != nil {
		return err
	}

	return nil
}

// makeFile will create a file with the specified content if it does not exist yet.
// if the file already exists, the method is a noop.
func makeFile(filePath, fileContents string) error {
	_, err := os.Stat(filePath)
	if err == nil {
		log.Printf("File exists: %q\n", filePath)
		return nil
	} else if os.IsNotExist(err) {
		log.Printf("Writing to: %q\n", filePath)
		return ioutil.WriteFile(filePath, []byte(fileContents), workingDirectoryPermission)
	} else {
		return err
	}
}

// load the docker compose file and then parse it as supervisor Services
// the logic for loading the compose file comes from the compose reference implementation
// https://github.com/compose-spec/compose-ref/blob/master/compose-ref.go#L353
func loadServiceDefinition(cfg upConfig) ([]pkg.Service, error) {

	serviceConfig, err := pkg.LoadComposeFile(cfg.workingDir, cfg.composeFilePath)
	if err != nil {
		return nil, err
	}

	return pkg.ParseCompose(serviceConfig)
}

// ConfigureUpFlags will define the flags for the `fecore up` command. The flag struct, configure, and
// parse are split like this to simplify testability.
func configureUpFlags(flags *flag.FlagSet) {
	flags.StringP("file", "f", "docker-compose.yaml", "compose file specifying the fecore service configuration")
}

// ParseUpFlags will load the flag values into an upFlags object. Errors will be underlying
// Get errors from the pflag library.
func parseUpFlags(cmd *cobra.Command) (upConfig, error) {
	parsed := upConfig{}
	path, err := cmd.Flags().GetString("file")
	if err != nil {
		return parsed, errors.Wrap(err, "can not parse compose file path flag")
	}

	parsed.composeFilePath = path
	parsed.workingDir = fecorewd
	return parsed, err
}

func startRedis(ctx context.Context) (*redis.Client, error) {
	// Check if Redis is already running
	cmd := exec.Command("pgrep", "redis-server")
	output, err := cmd.Output()
	if err == nil && len(output) > 0 {
		log.Println("Redis is already running. Stopping existing instance...")
		stopCmd := exec.Command("redis-cli", "shutdown")
		if err := stopCmd.Run(); err != nil {
			return nil, fmt.Errorf("failed to stop existing Redis: %w", err)
		}
		// Wait a moment for the server to fully stop
		time.Sleep(time.Second)
	}

	// Start Redis server without protected mode and binding to all interfaces
	cmd = exec.CommandContext(ctx, "redis-server",
		"--protected-mode", "no",
		"--bind", "0.0.0.0")

	// Capture stdout and stderr
	var outb, errb bytes.Buffer
	cmd.Stdout = &outb
	cmd.Stderr = &errb

	// Run Redis in the foreground
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start Redis: %w", err)
	}

	// Use a goroutine to wait for the command to finish and log its output
	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Printf("Redis server stopped with error: %v\nStdout: %s\nStderr: %s", err, outb.String(), errb.String())
		} else {
			log.Printf("Redis server stopped without error\nStdout: %s\nStderr: %s", outb.String(), errb.String())
		}
	}()

	// Give Redis a moment to start
	time.Sleep(time.Second)

	// Create a Redis client
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// Ping the Redis server to ensure it's running
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = client.Ping(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	log.Println("Redis started successfully without protection, bound to all interfaces")
	return client, nil
}
