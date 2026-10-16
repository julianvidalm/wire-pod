package mictap

// Tee returns a Sink that hands every call to each of sinks, in order.
func Tee(sinks ...Sink) Sink {
	return tee(sinks)
}

type tee []Sink

func (t tee) Start(esn string, h Hello) {
	for _, s := range t {
		s.Start(esn, h)
	}
}

func (t tee) Frame(esn string, f Frame) {
	for _, s := range t {
		s.Frame(esn, f)
	}
}

func (t tee) End(esn string, err error) {
	for _, s := range t {
		s.End(esn, err)
	}
}
