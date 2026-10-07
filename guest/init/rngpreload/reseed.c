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
 * Deliberately header-less and built with -nostdlib: the object has no libc
 * dependency, so one x86_64 ELF loads under both glibc and musl. The napi_*
 * and RAND_poll symbols resolve against the node process at dlopen time,
 * whether it links OpenSSL statically (official builds export it) or
 * dynamically (distro builds). Rebuild with `make rng-addon`.
 */
typedef struct napi_env__ *napi_env;
typedef struct napi_value__ *napi_value;
typedef struct napi_callback_info__ *napi_callback_info;
typedef int napi_status;
typedef napi_value (*napi_callback)(napi_env env, napi_callback_info info);

extern napi_status napi_get_boolean(napi_env env, _Bool value, napi_value *result);
extern napi_status napi_create_function(napi_env env, const char *utf8name, unsigned long length,
                                        napi_callback cb, void *data, napi_value *result);
extern napi_status napi_set_named_property(napi_env env, napi_value object, const char *utf8name,
                                           napi_value value);
extern int RAND_poll(void);

#define NAPI_AUTO_LENGTH ((unsigned long)-1)
#define EXPORT __attribute__((visibility("default")))

static napi_value poll_cb(napi_env env, napi_callback_info info) {
    (void)info;
    napi_value result = 0;
    napi_get_boolean(env, RAND_poll() == 1, &result);
    return result;
}

EXPORT int node_api_module_get_api_version_v1(void) { return 8; }

EXPORT napi_value napi_register_module_v1(napi_env env, napi_value exports) {
    napi_value fn = 0;
    if (napi_create_function(env, "poll", NAPI_AUTO_LENGTH, poll_cb, 0, &fn) != 0) {
        return exports;
    }
    napi_set_named_property(env, exports, "poll", fn);
    return exports;
}
