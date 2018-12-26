/*
Copyright 2016 Medcl (m AT medcl.net)

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

package env

import (
	"fmt"
	log "github.com/cihub/seelog"
	"github.com/infinitbyte/framework/core/config"
	"github.com/infinitbyte/framework/core/errors"
	"github.com/infinitbyte/framework/core/util"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

//TODO storage adaptor should config in env

// Env is environment object of app
type Env struct {
	name          string
	uppercaseName string
	desc          string
	version       string
	commit        string
	buildDate     string

	configFile string

	terminalHeader string
	terminalFooter string

	// static configs
	SystemConfig *config.SystemConfig

	IsDebug      bool
	IsDaemonMode bool

	LoggingLevel string

	init bool
}

// GetLastCommitLog returns last commit information of source code
func (env *Env) GetLastCommitLog() string {
	return env.commit
}

func (env *Env) GetLastCommitHash() string {
	log := env.GetLastCommitLog()
	array := strings.Split(log, ",")
	if len(array) == 0 {
		return "N/A"
	}
	return array[0]
}

// GetBuildDate returns the build datetime of current package
func (env *Env) GetBuildDate() string {
	return env.buildDate
}

// GetVersion returns the version of this build
func (env *Env) GetVersion() string {
	return env.version
}

func (env *Env) GetAppName() string {
	return env.name
}

func (env *Env) GetAppCapitalName() string {
	return env.uppercaseName
}

func (env *Env) GetAppDesc() string {
	return env.desc
}

func (env *Env) GetWelcomeMessage() string {
	s := env.terminalHeader

	commitLog := ""
	if env.GetLastCommitLog() != "" {
		commitLog = " " + env.GetLastCommitLog()
	}
	s += ("[" + env.GetAppCapitalName() + "] " + env.GetAppDesc() + "\n")
	s += env.GetVersion() + ", " + commitLog + "\n"
	return s
}

func (env *Env) GetGoodbyeMessage() string {
	s := env.terminalFooter

	if env.IsDaemonMode {
		return s
	}

	s += fmt.Sprintf("[%s] %s, uptime:%s\n", env.GetAppCapitalName(), env.GetVersion(), time.Since(GetStartTime()))
	return s
}

// Environment create a new env instance from a config
func (env *Env) Init() *Env {
	if env.init {
		return env
	}

	err := env.loadConfig()
	if err != nil {
		panic(err)
	}
	os.MkdirAll(env.GetWorkingDir(), 0777)
	os.MkdirAll(env.SystemConfig.PathConfig.Log, 0777)

	if env.IsDebug {
		log.Debug(util.ToJson(env, true))
	}

	env.init = true
	return env
}

var moduleConfig map[string]*config.Config
var pluginConfig map[string]*config.Config
var startTime = time.Now().UTC()

var (
	defaultSystemConfig = config.SystemConfig{
		ClusterConfig: config.ClusterConfig{
			Name: "app",
		},
		NetworkConfig: config.NetworkConfig{
			Host:           "127.0.0.1",
			APIBinding:     "127.0.0.1:8000",
			HTTPBinding:    "127.0.0.1:9000",
			ClusterBinding: "127.0.0.1:10000",
		},
		NodeConfig: config.NodeConfig{
			Name: util.PickRandomName(),
		},
		PathConfig: config.PathConfig{
			Data: "data",
			Log:  "log",
			Cert: "cert",
		},

		AllowMultiInstance: true,
		MaxNumOfInstance:   5,
		TLSEnabled:         false,
	}
)

var configObject *config.Config

func (env *Env) loadConfig() error {

	var ignoreFileMissing bool
	if env.configFile == "" {
		env.configFile = "./app.yml"
		ignoreFileMissing = true
	}

	filename, _ := filepath.Abs(env.configFile)

	if util.FileExists(filename) {
		env.SystemConfig = &defaultSystemConfig

		log.Debug("load file:", filename)
		var err error
		configObject, err = config.LoadFile(filename)
		if err != nil {
			return err
		}

		if err := configObject.Unpack(env.SystemConfig); err != nil {
			return err
		}

		pluginConfig = parseModuleConfig(env.SystemConfig.Plugins)
		moduleConfig = parseModuleConfig(env.SystemConfig.Modules)

	} else {
		if !ignoreFileMissing {
			return errors.Errorf("no config was found: %s", filename)
		}
	}

	return nil
}

func (env *Env) SetConfigFile(configFile string) *Env {
	env.configFile = configFile
	return env
}

func parseModuleConfig(cfgs []*config.Config) map[string]*config.Config {
	result := map[string]*config.Config{}

	for _, cfg := range cfgs {
		log.Trace(getModuleName(cfg), ",", cfg.Enabled(true))
		name := getModuleName(cfg)
		result[name] = cfg
	}

	return result
}

//GetModuleConfig return specify module's config
func GetModuleConfig(name string) *config.Config {
	cfg := moduleConfig[strings.ToLower(name)]
	return cfg
}

//GetPluginConfig return specify plugin's config
func GetPluginConfig(name string) *config.Config {
	cfg := pluginConfig[strings.ToLower(name)]
	return cfg
}

func GetConfig(configKey string, configInstance interface{}) error {

	if configObject != nil {
		childConfig, err := configObject.Child(configKey, -1)
		if err != nil {
			return err
		}

		err = childConfig.Unpack(configInstance)
		if err != nil {
			return err
		}

		return nil
	} else {
		log.Errorf("config is nil")
	}
	return errors.Errorf("invalid config: %s", configKey)
}

func getModuleName(c *config.Config) string {
	cfgObj := struct {
		Module string `config:"name"`
	}{}

	if c == nil {
		return ""
	}
	if err := c.Unpack(&cfgObj); err != nil {
		return ""
	}

	return cfgObj.Module
}

// EmptyEnv return a empty env instance
func NewEnv(name, desc, ver, commit, buildDate, terminalHeader, terminalFooter string) *Env {
	return &Env{
		name:           util.TrimSpaces(name),
		uppercaseName:  strings.ToUpper(util.TrimSpaces(name)),
		desc:           util.TrimSpaces(desc),
		version:        util.TrimSpaces(ver),
		commit:         util.TrimSpaces(commit),
		buildDate:      buildDate,
		terminalHeader: terminalHeader,
		terminalFooter: terminalFooter,
	}
}

func EmptyEnv() *Env {
	system := defaultSystemConfig
	return &Env{SystemConfig: &system}
}

func GetStartTime() time.Time {
	return startTime
}

var workingDir = ""

// GetWorkingDir returns root working dir of app instance
func (env *Env) GetWorkingDir() string {
	if workingDir != "" {
		return workingDir
	}

	if !env.SystemConfig.AllowMultiInstance {
		workingDir = path.Join(env.SystemConfig.PathConfig.Data, env.SystemConfig.ClusterConfig.Name, "nodes", "0")
		return workingDir
	}

	//auto select next nodes folder, eg: nodes/1 nodes/2
	i := 0
	if env.SystemConfig.MaxNumOfInstance < 1 {
		env.SystemConfig.MaxNumOfInstance = 5
	}
	for j := 0; j < env.SystemConfig.MaxNumOfInstance; j++ {
		p := path.Join(env.SystemConfig.PathConfig.Data, env.SystemConfig.ClusterConfig.Name, "nodes", util.IntToString(i))
		lockFile := path.Join(p, ".lock")
		if !util.FileExists(lockFile) {
			workingDir = p
			return workingDir
		}

		//check if pid is alive
		b, err := ioutil.ReadFile(lockFile)
		if err != nil {
			panic(err)
		}
		pid, err := util.ToInt(string(b))
		if err != nil {
			panic(err)
		}
		if pid <= 0 {
			panic(errors.New("invalid pid"))
		}

		procExists := util.CheckProcessExists(pid)
		if !procExists {
			util.FileDelete(lockFile)
			log.Debug("dead process with broken lock file, removed: ", lockFile)
			workingDir = p
			return p
		}

		i++
	}
	panic(fmt.Errorf("reach max num of instances on this node, limit is: %v", env.SystemConfig.MaxNumOfInstance))

}
