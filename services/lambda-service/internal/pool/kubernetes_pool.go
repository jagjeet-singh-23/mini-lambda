package pool

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/jagjeet-singh-23/mini-lambda/shared/domain"
	uuid "github.com/satori/go.uuid"
)

type KubernetesPodPool struct {
	config     PoolConfig
	client     kubernetes.Interface
	namespace  string
	baseImage  string
	containers []*Container // Using Container struct from pool.go
	mu         sync.RWMutex
	stats      poolMetrics
	statsMu    sync.Mutex
	createdAt  time.Time
}

func NewKubernetesPodPool(config PoolConfig, baseImage string) (*KubernetesPodPool, error) {
	// In-cluster config for when running inside a Pod
	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config (not running in k8s?): %w", err)
	}

	clientset, err := kubernetes.NewForConfig(clusterConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %w", err)
	}

	// Read namespace from file or use default
	namespace := "mini-lambda"

	pool := &KubernetesPodPool{
		config:     config,
		client:     clientset,
		namespace:  namespace,
		baseImage:  baseImage,
		containers: make([]*Container, 0, config.MaxSize),
		createdAt:  time.Now(),
	}

	return pool, nil
}

func (p *KubernetesPodPool) Acquire(ctx context.Context) (*Container, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.containers) == 0 {
		return nil, nil
	}

	for i, container := range p.containers {
		if container.State != StateWarm {
			continue
		}

		container.State = StateInUse
		container.LastUsed = time.Now()
		container.UseCount++

		p.moveToEnd(i)

		p.updateStats(func(m *poolMetrics) {
			m.warmStarts++
		})

		return container, nil
	}

	return nil, nil
}

func (p *KubernetesPodPool) Release(ctx context.Context, container *Container) error {
	if container == nil {
		return fmt.Errorf("cannot release nil container")
	}

	// HTTP execution approach doesn't easily allow for file system cleanup
	// We'll rely on the LRU eviction and memory bounds for now.
	container.State = StateCleaning
	// In a real implementation, you might make a /reset HTTP call here

	p.mu.Lock()
	defer p.mu.Unlock()

	// If we've hit max use count, delete the pod
	if p.config.MaxUseCount > 0 && container.UseCount >= p.config.MaxUseCount {
		if err := p.removePodUnsafe(ctx, container); err != nil {
			return err
		}
		return nil
	}

	container.State = StateWarm
	container.LastUsed = time.Now()

	return nil
}

func (p *KubernetesPodPool) CreateNew(ctx context.Context) (*Container, error) {
	p.mu.RLock()
	atCapacity := len(p.containers) >= p.config.MaxSize
	p.mu.RUnlock()

	if atCapacity {
		if err := p.Evict(ctx); err != nil {
			return nil, fmt.Errorf("failed to evict container: %w", err)
		}
	}

	podID, podIP, err := p.createPod(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}

	newContainer := &Container{
		ID:         podID, // Pod Name
		Runtime:    podIP, // Hack: Storing the pod IP in the Runtime field to avoid changing the shared Struct
		CreatedAt:  time.Now(),
		LastUsed:   time.Now(),
		UseCount:   0,
		State:      StateWarm,
		MemoryUsed: 0,
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.containers = append(p.containers, newContainer)

	p.updateStats(func(m *poolMetrics) {
		m.coldStarts++
		m.totalCreated++
	})

	return newContainer, nil
}

func (p *KubernetesPodPool) Evict(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.containers) == 0 {
		return fmt.Errorf("pool is empty, nothing to evict")
	}

	var lruIndex int
	var oldestTime time.Time = time.Now()

	for i, c := range p.containers {
		if c.State == StateWarm && c.LastUsed.Before(oldestTime) {
			lruIndex = i
			oldestTime = c.LastUsed
		}
	}

	lruContainer := p.containers[lruIndex]
	return p.removePodUnsafe(ctx, lruContainer)
}

func (p *KubernetesPodPool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.containers)
}

// GetClient returns the underlying Kubernetes clientset
func (p *KubernetesPodPool) GetClient() kubernetes.Interface {
	return p.client
}

func (p *KubernetesPodPool) Stats() domain.PoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	p.statsMu.Lock()
	defer p.statsMu.Unlock()

	var warmCount, inUseCount int
	var totalUseCount int64

	for _, container := range p.containers {
		switch container.State {
		case StateWarm:
			warmCount++
		case StateInUse:
			inUseCount++
		}
		totalUseCount += container.UseCount
	}

	totalRequests := p.stats.coldStarts + p.stats.warmStarts
	var hitRate float64
	if totalRequests > 0 {
		hitRate = float64(p.stats.warmStarts) / float64(totalRequests) * 100
	}

	var avgUseCount float64
	if len(p.containers) > 0 {
		avgUseCount = float64(totalUseCount) / float64(len(p.containers))
	}

	return domain.PoolStats{
		Runtime:         p.config.Runtime,
		TotalContainers: len(p.containers),
		WarmContainers:  warmCount,
		InUseContainers: inUseCount,
		HitRate:         hitRate,
		ColdStarts:      p.stats.coldStarts,
		WarmStarts:      p.stats.warmStarts,
		TotalEvictions:  p.stats.totalEvictions,
		AverageUseCount: avgUseCount,
		CreatedAt:       p.createdAt,
	}
}

func (p *KubernetesPodPool) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var errors []error

	for _, container := range p.containers {
		// Try to delete the pod
		err := p.client.CoreV1().Pods(p.namespace).Delete(ctx, container.ID, metav1.DeleteOptions{})
		if err != nil {
			errors = append(errors, err)
		}
	}

	p.containers = nil
	if len(errors) > 0 {
		return fmt.Errorf("failed to stop pods: %v", errors)
	}

	return nil
}

func (p *KubernetesPodPool) Start(_ context.Context) {}

func (p *KubernetesPodPool) moveToEnd(index int) {
	if index == len(p.containers)-1 {
		return
	}
	container := p.containers[index]
	copy(p.containers[index:], p.containers[index+1:])
	p.containers[len(p.containers)-1] = container
}

func (p *KubernetesPodPool) removePodUnsafe(ctx context.Context, container *Container) error {
	// Send delete request
	err := p.client.CoreV1().Pods(p.namespace).Delete(ctx, container.ID, metav1.DeleteOptions{})
	if err != nil {
		fmt.Printf("Failed to delete pod %s: %v\n", container.ID, err)
		// We still remove it from the slice even if delete fails
	}

	for i, c := range p.containers {
		if c.ID == container.ID {
			p.containers[i] = p.containers[len(p.containers)-1]
			p.containers = p.containers[:len(p.containers)-1]
			break
		}
	}

	p.updateStats(func(m *poolMetrics) {
		m.totalEvictions++
	})

	return nil
}

func (p *KubernetesPodPool) createPod(ctx context.Context) (string, string, error) {
	podName := fmt.Sprintf("runner-%s-%s", p.config.Runtime, uuid.NewV4().String()[:8])

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: p.namespace,
			Labels: map[string]string{
				"app":     "lambda-runner",
				"runtime": p.config.Runtime,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  "runner",
					Image: p.baseImage,
					Ports: []corev1.ContainerPort{
						{ContainerPort: 8080},
					},
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("256Mi"),
							corev1.ResourceCPU:    resource.MustParse("250m"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("128Mi"),
							corev1.ResourceCPU:    resource.MustParse("100m"),
						},
					},
				},
			},
		},
	}

	// Create the pod
	createdPod, err := p.client.CoreV1().Pods(p.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return "", "", fmt.Errorf("failed to create pod: %w", err)
	}

	// Wait for pod to be running and get IP
	watcher, err := p.client.CoreV1().Pods(p.namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", createdPod.Name),
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to watch pod: %w", err)
	}
	defer watcher.Stop()

	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for {
		select {
		case event := <-watcher.ResultChan():
			p, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			if p.Status.Phase == corev1.PodRunning && p.Status.PodIP != "" {
				return p.Name, p.Status.PodIP, nil
			}
			if p.Status.Phase == corev1.PodFailed || p.Status.Phase == corev1.PodUnknown {
				return "", "", fmt.Errorf("pod failed to start, phase: %s", p.Status.Phase)
			}
		case <-timeoutCtx.Done():
			return "", "", fmt.Errorf("timeout waiting for pod to start")
		}
	}
}

func (p *KubernetesPodPool) updateStats(fn func(*poolMetrics)) {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	fn(&p.stats)
}
