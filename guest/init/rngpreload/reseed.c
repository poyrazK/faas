/*
 * Restore reseed addon (GHSA-24j2-p895-mwc9).
 *
 * A snapshot-restored process resumes with OpenSSL's DRBG state from the
 * moment of capture, so every restore of one snapshot, and every sibling
 * instance, replays the same crypto.randomBytes/TLS randomness. Node has no
 * JavaScript API that reseeds OpenSSL; this module exposes one: poll() calls
 * RAND_poll(), which reseeds the primary DRBG from the (already reseeded)
 * kernel, and OpenSSL 3 propagates the reseed to every thread's child DRBG.
 *
 * start() is the thread barrier client (ADR-20261024105607). It starts one
 * native thread per process that registers on guest-init's reseed socket
 * itself and answers the barrier without the event loop: RAND_poll(), then
 * a bump of the process-wide restore generation that shared() exposes to
 * JavaScript, then "ok". The preload reseeds Math.random and marks the RNG
 * caches stale when it sees a new generation, before the next value it
 * returns. The thread touches a few pages instead of faulting the event loop
 * and the JavaScript heap back in.
 *
 * Deliberately header-less and built with -nostdlib: the object has no libc
 * dependency, so one x86_64 ELF loads under both glibc and musl. The napi_*,
 * RAND_poll, pthread and socket symbols resolve against the node process and
 * its libc at dlopen time, whether node links OpenSSL statically (official
 * builds export it) or dynamically (distro builds). Rebuild with
 * `make rng-addon`.
 */
typedef struct napi_env__ *napi_env;
typedef struct napi_value__ *napi_value;
typedef struct napi_callback_info__ *napi_callback_info;
typedef int napi_status;
typedef napi_value (*napi_callback)(napi_env env, napi_callback_info info);
typedef void (*napi_finalize)(napi_env env, void *data, void *hint);

extern napi_status napi_get_boolean(napi_env env, _Bool value, napi_value *result);
extern napi_status napi_create_function(napi_env env, const char *utf8name, unsigned long length,
                                        napi_callback cb, void *data, napi_value *result);
extern napi_status napi_set_named_property(napi_env env, napi_value object, const char *utf8name,
                                           napi_value value);
extern napi_status napi_get_cb_info(napi_env env, napi_callback_info info, unsigned long *argc,
                                    napi_value *argv, napi_value *this_arg, void **data);
extern napi_status napi_get_value_string_utf8(napi_env env, napi_value value, char *buf,
                                              unsigned long bufsize, unsigned long *result);
extern napi_status napi_create_int32(napi_env env, int value, napi_value *result);
extern napi_status napi_create_external_arraybuffer(napi_env env, void *data, unsigned long length,
                                                    napi_finalize finalize, void *hint,
                                                    napi_value *result);
extern int RAND_poll(void);

extern int pthread_attr_init(void *attr);
extern int pthread_attr_setstacksize(void *attr, unsigned long size);
extern int pthread_attr_setdetachstate(void *attr, int state);
extern int pthread_attr_destroy(void *attr);
extern int pthread_create(void *thread, const void *attr, void *(*start)(void *), void *arg);
extern int pthread_sigmask(int how, const void *set, void *old);
extern int socket(int domain, int type, int protocol);
extern int connect(int fd, const void *addr, unsigned int len);
extern int pipe(int fds[2]);
extern int fcntl(int fd, int cmd, ...);
extern long read(int fd, void *buf, unsigned long len);
extern long write(int fd, const void *buf, unsigned long len);
extern int close(int fd);
extern int getpid(void);

#define NAPI_AUTO_LENGTH ((unsigned long)-1)
#define EXPORT __attribute__((visibility("default")))

#define AF_UNIX 1
#define SOCK_STREAM 1
#define F_SETFD 2
#define F_GETFL 3
#define F_SETFL 4
#define FD_CLOEXEC 1
#ifdef __APPLE__
#define O_NONBLOCK 0x0004
#define SIG_BLOCK 1
#define SIG_SETMASK 3
#define PTHREAD_CREATE_DETACHED 2
#else
#define O_NONBLOCK 0x0800
#define SIG_BLOCK 0
#define SIG_SETMASK 2
#define PTHREAD_CREATE_DETACHED 1
#endif

#define RESEED_MAX_LINE 256
#define RESEED_MIN_NONCE_HEX 32
#define RESEED_THREAD_STACK (256 * 1024)

/* shared[0] is the restore generation: the thread bumps it after RAND_poll()
 * and before it answers "ok". shared[1] is 1 while the thread is registered.
 * Every isolate in the process maps the same words through shared(). */
static unsigned int shared[4] __attribute__((aligned(16)));
static int started;
static int sock_fd = -1;
static int notify_fd = -1;

static napi_value poll_cb(napi_env env, napi_callback_info info) {
    (void)info;
    napi_value result = 0;
    napi_get_boolean(env, RAND_poll() == 1, &result);
    return result;
}

static int write_all(int fd, const char *buf, unsigned long len) {
    while (len > 0) {
        long n = write(fd, buf, len);
        if (n <= 0) {
            return -1;
        }
        buf += n;
        len -= (unsigned long)n;
    }
    return 0;
}

static int is_hex(char c) {
    return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F');
}

/* answer handles one barrier line. Anything but "reseed <hex>" is ignored,
 * as the JavaScript client does. */
static void answer(const char *line, unsigned long len) {
    static const char prefix[] = "reseed ";
    unsigned long i;
    unsigned long hex = 0;
    for (i = 0; i < sizeof(prefix) - 1; i++) {
        if (i >= len || line[i] != prefix[i]) {
            return;
        }
    }
    for (; i < len && is_hex(line[i]); i++) {
        hex++;
    }
    if (i != len || hex < RESEED_MIN_NONCE_HEX || hex % 2 != 0) {
        write_all(sock_fd, "err bad_nonce\n", 14);
        return;
    }
    if (RAND_poll() != 1) {
        write_all(sock_fd, "err openssl_reseed_failed\n", 26);
        return;
    }
    __atomic_add_fetch(&shared[0], 1, __ATOMIC_SEQ_CST);
    if (write_all(sock_fd, "ok\n", 3) == 0 && notify_fd >= 0) {
        char wake = 1;
        (void)write(notify_fd, &wake, 1); /* a full pipe already holds a wake */
    }
}

static void *reseed_thread(void *arg) {
    (void)arg;
    char buf[RESEED_MAX_LINE];
    unsigned long used = 0;
    for (;;) {
        long n = read(sock_fd, buf + used, sizeof(buf) - used);
        if (n <= 0) {
            break; /* guest-init closed the socket: nothing left to answer */
        }
        used += (unsigned long)n;
        unsigned long start = 0;
        for (unsigned long i = 0; i < used; i++) {
            if (buf[i] == '\n') {
                unsigned long end = i;
                while (end > start && (buf[end - 1] == '\r' || buf[end - 1] == ' ')) {
                    end--;
                }
                answer(buf + start, end - start);
                start = i + 1;
            }
        }
        if (start == 0 && used == sizeof(buf)) {
            break; /* no newline in a full buffer: not guest-init */
        }
        for (unsigned long i = start; i < used; i++) {
            buf[i - start] = buf[i];
        }
        used -= start;
    }
    __atomic_store_n(&shared[1], 0, __ATOMIC_SEQ_CST);
    close(sock_fd);
    sock_fd = -1;
    return 0;
}

static int cloexec(int fd) {
    return fcntl(fd, F_SETFD, FD_CLOEXEC);
}

static int format_hello(char *out, int pid) {
    static const char prefix[] = "hello node-thread ";
    char digits[16];
    int n = 0, i, len = 0;
    for (i = 0; prefix[i]; i++) {
        out[len++] = prefix[i];
    }
    do {
        digits[n++] = (char)('0' + pid % 10);
        pid /= 10;
    } while (pid > 0 && n < (int)sizeof(digits));
    while (n > 0) {
        out[len++] = digits[--n];
    }
    out[len++] = '\n';
    return len;
}

/* connect_reseed_socket connects and registers synchronously, so a process
 * that start() returned for is registered before it serves anything. */
static int connect_reseed_socket(const char *path, unsigned long path_len) {
#ifdef __APPLE__
    struct {
        unsigned char len;
        unsigned char family;
        char path[104];
    } addr = {0};
    addr.family = AF_UNIX;
#else
    struct {
        unsigned short family;
        char path[108];
    } addr = {0};
    addr.family = AF_UNIX;
#endif
    if (path_len == 0 || path_len >= sizeof(addr.path)) {
        return -1;
    }
    for (unsigned long i = 0; i < path_len; i++) {
        addr.path[i] = path[i];
    }
#ifdef __APPLE__
    addr.len = (unsigned char)sizeof(addr);
#endif
    int fd = socket(AF_UNIX, SOCK_STREAM, 0);
    if (fd < 0) {
        return -1;
    }
    char hello[48];
    int hello_len = format_hello(hello, getpid());
    if (cloexec(fd) != 0 || connect(fd, &addr, sizeof(addr)) != 0 ||
        write_all(fd, hello, (unsigned long)hello_len) != 0) {
        close(fd);
        return -1;
    }
    return fd;
}

/* start(socketPath) registers this process and starts its reseed thread.
 * It returns the read end of a pipe that receives one byte after each
 * reseed, or -1. Only the first call in a process starts a thread. */
static napi_value start_cb(napi_env env, napi_callback_info info) {
    unsigned long argc = 1;
    napi_value argv[1] = {0};
    napi_value result = 0;
    char path[128];
    unsigned long path_len = 0;
    int out = -1;
    if (__atomic_exchange_n(&started, 1, __ATOMIC_SEQ_CST) != 0) {
        napi_create_int32(env, -1, &result);
        return result;
    }
    if (napi_get_cb_info(env, info, &argc, argv, 0, 0) == 0 && argc == 1 &&
        napi_get_value_string_utf8(env, argv[0], path, sizeof(path), &path_len) == 0 &&
        path_len < sizeof(path) - 1) {
        int fds[2];
        sock_fd = connect_reseed_socket(path, path_len);
        if (sock_fd >= 0 && pipe(fds) != 0) {
            close(sock_fd);
            sock_fd = -1;
        }
        if (sock_fd >= 0) {
            int flags = fcntl(fds[1], F_GETFL);
            cloexec(fds[0]);
            cloexec(fds[1]);
            if (flags >= 0) {
                fcntl(fds[1], F_SETFL, flags | O_NONBLOCK);
            }
            notify_fd = fds[1];
            out = fds[0];
            /* Block every signal the C library lets callers block, so
             * process-directed signals go to the runtime's own threads. */
            unsigned long all[16] = {0}, old[16] = {0};
            unsigned long attr[16] = {0};
            unsigned long thread = 0;
#ifdef __APPLE__
            all[0] = 0xffffffffUL;
#else
            all[0] = 0xfffffffc7fffffffUL; /* not 32-34: libc-internal */
#endif
            pthread_sigmask(SIG_BLOCK, all, old);
            int ok = pthread_attr_init(attr) == 0;
            if (ok) {
                pthread_attr_setstacksize(attr, RESEED_THREAD_STACK);
                pthread_attr_setdetachstate(attr, PTHREAD_CREATE_DETACHED);
                __atomic_store_n(&shared[1], 1, __ATOMIC_SEQ_CST);
                ok = pthread_create(&thread, attr, reseed_thread, 0) == 0;
                pthread_attr_destroy(attr);
            }
            pthread_sigmask(SIG_SETMASK, old, 0);
            if (!ok) {
                close(sock_fd);
                close(fds[0]);
                close(fds[1]);
                sock_fd = notify_fd = out = -1;
                __atomic_store_n(&shared[1], 0, __ATOMIC_SEQ_CST);
            }
        }
    }
    napi_create_int32(env, out, &result);
    return result;
}

/* shared() exposes the restore generation words as an ArrayBuffer. */
static napi_value shared_cb(napi_env env, napi_callback_info info) {
    (void)info;
    napi_value result = 0;
    if (napi_create_external_arraybuffer(env, shared, sizeof(shared), 0, 0, &result) != 0) {
        return 0;
    }
    return result;
}

EXPORT int node_api_module_get_api_version_v1(void) { return 8; }

static void export_fn(napi_env env, napi_value exports, const char *name, napi_callback cb) {
    napi_value fn = 0;
    if (napi_create_function(env, name, NAPI_AUTO_LENGTH, cb, 0, &fn) == 0) {
        napi_set_named_property(env, exports, name, fn);
    }
}

EXPORT napi_value napi_register_module_v1(napi_env env, napi_value exports) {
    export_fn(env, exports, "poll", poll_cb);
    export_fn(env, exports, "start", start_cb);
    export_fn(env, exports, "shared", shared_cb);
    return exports;
}
