package proc

type Proc interface {
	Name() string
	Status() (string, bool)
}
