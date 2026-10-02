package cell

// kalman1 is a constant-velocity Kalman filter along one axis: state
// (position, velocity), measuring position only.
type kalman1 struct {
	x, v          float64
	pxx, pxv, pvv float64 // covariance
}

// Noise levels: centroids jump by a pixel or two as a cell changes shape,
// and real cells accelerate slowly.
const (
	measVar  = 4.0   // px²
	accelVar = 0.002 // (px/min²)² of random acceleration
)

func newKalman1(x0, v0 float64) kalman1 {
	return kalman1{x: x0, v: v0, pxx: measVar, pvv: 1}
}

// step predicts dt minutes ahead and updates with measurement z.
func (k *kalman1) step(dt, z float64) {
	// Predict.
	k.x += k.v * dt
	q := accelVar
	pxx := k.pxx + 2*dt*k.pxv + dt*dt*k.pvv + q*dt*dt*dt/3
	pxv := k.pxv + dt*k.pvv + q*dt*dt/2
	pvv := k.pvv + q*dt
	// Update.
	s := pxx + measVar
	kx, kv := pxx/s, pxv/s
	r := z - k.x
	k.x += kx * r
	k.v += kv * r
	k.pxx, k.pxv, k.pvv = (1-kx)*pxx, (1-kx)*pxv, pvv-kv*pxv
}

// trackVelocity filters a track of centroids dt minutes apart, oldest
// first, starting from velocity guess (v0x, v0y); it returns the smoothed
// velocity at the last point in pixels per minute.
func trackVelocity(xs, ys []float64, dt, v0x, v0y float64) (vx, vy float64) {
	kx, ky := newKalman1(xs[0], v0x), newKalman1(ys[0], v0y)
	for i := 1; i < len(xs); i++ {
		kx.step(dt, xs[i])
		ky.step(dt, ys[i])
	}
	return kx.v, ky.v
}
