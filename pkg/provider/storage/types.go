// TODO starter code, needs to be rearrange and integrated

package storage

type Function struct {
	Name		string // unique
	Namespace	string
	Image		string
	Labels		string // json
	Annotations	string // json
	Secret		string // json
	SecretsPath	string
	EnvVars		string // json
	EnvProcess	string
	MemoryLimit	string
}

type Container struct {
	Uuid			string // unique
	ParentFunction	string
}

type StorageManager interface {
	InsertFunction(function Function) error
	GetAllFunctions() ([]Function, error)
	DeleteFunction(name string) error

	InsertContainer(container Container) error
	GetAllContainers() ([]Container, error)
	DeleteContainer(uuid string) error
}