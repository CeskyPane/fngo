package logging

type Field struct {
	Key   string
	Value any
}

type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
}

type NopLogger struct{}

func (NopLogger) Debug(string, ...Field) {}

func (NopLogger) Info(string, ...Field) {}

func (NopLogger) Warn(string, ...Field) {}

func (NopLogger) Error(string, ...Field) {}

func With(logger Logger) Logger {
	if logger == nil {
		return NopLogger{}
	}

	return logger
}

func F(key string, value any) Field {
	return Field{Key: key, Value: value}
}
