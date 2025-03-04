package pkg

import "context"

func fn1(ctx context.Context)           {}
func fn2(x string, ctx context.Context) {}
func fn4()                              {}

type T struct{}

func (*T) Foo() {}

func fn3() {
	fn1(nil) // want `do not pass a nil Context`
	fn1(context.TODO())
	fn2("", nil)
	fn4()

	// don't flag this conversion
	_ = (func(context.Context))(nil)
	// and don't crash on these
	_ = (func())(nil)
	(*T).Foo(nil)
}
