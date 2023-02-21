// TODO starter code, needs to be rearrange and integrated

package storage

type Function struct {
	Name		string // unique
	namespace	string
	image		string
	labels		string // json
	annotations	string // json
	secret		string // json
	secretsPath	string
	envVars		string // json
	envProcess	string
	memoryLimit	string
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