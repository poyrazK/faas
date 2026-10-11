// adr: 712
package durableentity

import "context"

type plannedObject struct {
	key  string
	body []byte
}

// Stage immutable uploads in memory to check the exact projected footprint
// before dispatching any write. The radix path bounds the upload plan.
type commitPlan struct {
	ObjectStore
	objects []plannedObject
}

func (p *commitPlan) Put(ctx context.Context, key string, body []byte, etag string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if etag != "" {
		return "", ErrInvalid
	}
	p.objects = append(p.objects, plannedObject{key: key, body: body})
	return digest(body), nil
}

func (p *commitPlan) upload(ctx context.Context) error {
	for _, object := range p.objects {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := p.ObjectStore.Put(ctx, object.key, object.body, ""); err != nil {
			return writeFailure("upload planned entity object", err)
		}
	}
	return nil
}
