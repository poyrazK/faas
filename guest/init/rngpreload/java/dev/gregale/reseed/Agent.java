// Gregale restore reseed agent (ADR-20261110150000, amends ADR-680/687).
//
// guest-init appends -javaagent:/run/guest-init/rng/java-reseed.jar to
// JAVA_TOOL_OPTIONS. A snapshot restores a JVM with the random state it held
// at capture, so every restore of one snapshot would replay Math.random,
// ThreadLocalRandom and the seeds of Random objects created after the restore.
// On resume guest-init sends each registered process a fresh nonce; this agent
// resets the JDK's shared generators and replies "ok" before the instance
// serves traffic.
//
// Compiled for Java 8 so every JVM can load it: a JVM refuses to start when an
// agent it cannot load is named. Every newer API is reached by reflection, and
// every failure is caught, so the agent never stops a JVM from starting. It
// registers only after it has proved it can reach every generator it resets.
package dev.gregale.reseed;

import java.io.BufferedReader;
import java.io.FileInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.lang.instrument.Instrumentation;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.net.ProtocolFamily;
import java.net.SocketAddress;
import java.nio.ByteBuffer;
import java.nio.channels.Channels;
import java.nio.channels.SocketChannel;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Random;
import java.util.Set;
import java.util.SplittableRandom;
import java.util.concurrent.ThreadLocalRandom;
import java.util.concurrent.atomic.AtomicLong;

public final class Agent {
    private Agent() {}

    public static void premain(String args, Instrumentation inst) {
        start(inst);
    }

    public static void agentmain(String args, Instrumentation inst) {
        start(inst);
    }

    private static void start(final Instrumentation inst) {
        try {
            final String path = System.getenv("GREGALE_RESEED_SOCKET");
            if (path == null || path.isEmpty() || "off".equalsIgnoreCase(System.getenv("GREGALE_RESTORE_RESEED"))) {
                return;
            }
            Thread thread = new Thread(new Runnable() {
                public void run() {
                    serve(inst, path);
                }
            }, "gregale-restore-reseed");
            thread.setDaemon(true);
            thread.start();
        } catch (Throwable ignored) {
            // Never stop the JVM: an unregistered process is reported instead.
        }
    }

    private static void serve(Instrumentation inst, String path) {
        try {
            Targets targets = Targets.open(inst);
            if (targets == null) {
                return;
            }
            SocketChannel channel = connect(path);
            if (channel == null) {
                return;
            }
            write(channel, "hello java " + pid() + "\n");
            BufferedReader reader = new BufferedReader(new InputStreamReader(Channels.newInputStream(channel), StandardCharsets.US_ASCII));
            String line;
            while ((line = reader.readLine()) != null) {
                line = line.trim();
                if (!line.startsWith("reseed ")) {
                    continue;
                }
                String reply;
                try {
                    reply = targets.reseed(line.substring("reseed ".length()).trim());
                } catch (Throwable err) {
                    reply = "err " + err.getClass().getSimpleName();
                }
                write(channel, reply + "\n");
            }
        } catch (Throwable ignored) {
            // The connection or the JVM is going away.
        }
    }

    // connect opens the barrier socket. Unix-domain SocketChannels arrived in
    // Java 16; an older JVM returns null and is reported as unregistered.
    private static SocketChannel connect(String path) throws Exception {
        Class<?> addressClass;
        try {
            addressClass = Class.forName("java.net.UnixDomainSocketAddress");
        } catch (ClassNotFoundException old) {
            return null;
        }
        SocketAddress address = (SocketAddress) addressClass.getMethod("of", String.class).invoke(null, path);
        @SuppressWarnings({"unchecked", "rawtypes"})
        Object unix = Enum.valueOf((Class) Class.forName("java.net.StandardProtocolFamily"), "UNIX");
        SocketChannel channel = (SocketChannel) SocketChannel.class.getMethod("open", ProtocolFamily.class).invoke(null, unix);
        channel.connect(address);
        return channel;
    }

    private static void write(SocketChannel channel, String text) throws IOException {
        ByteBuffer buf = ByteBuffer.wrap(text.getBytes(StandardCharsets.US_ASCII));
        while (buf.hasRemaining()) {
            channel.write(buf);
        }
    }

    private static long pid() throws Exception {
        try {
            Class<?> handle = Class.forName("java.lang.ProcessHandle");
            Object current = handle.getMethod("current").invoke(null);
            return ((Long) handle.getMethod("pid").invoke(current)).longValue();
        } catch (ClassNotFoundException java8) {
            return Long.parseLong(new java.io.File("/proc/self").getCanonicalFile().getName());
        }
    }

    // Targets holds the JDK generators the agent resets, resolved once before
    // registering. A field this JVM does not have is left out; one it has but
    // the agent cannot open keeps the agent from registering at all.
    static final class Targets {
        private final List<Random> holders = new ArrayList<Random>();
        private final List<AtomicLong> seeders = new ArrayList<AtomicLong>();
        private Field threadSeed;
        private Field threadProbe;

        static Targets open(Instrumentation inst) throws Exception {
            openJavaBase(inst);
            Targets t = new Targets();
            for (String holder : new String[] {"java.lang.Math$RandomNumberGeneratorHolder", "java.lang.StrictMath$RandomNumberGeneratorHolder"}) {
                Field f = field(holder, "randomNumberGenerator");
                if (f != null) {
                    t.holders.add((Random) f.get(null));
                }
            }
            t.addSeeder(Random.class, "seedUniquifier");
            t.addSeeder(ThreadLocalRandom.class, "seeder");
            t.addSeeder(SplittableRandom.class, "defaultGen");
            t.threadSeed = field(Thread.class.getName(), "threadLocalRandomSeed");
            t.threadProbe = field(Thread.class.getName(), "threadLocalRandomProbe");
            if (t.threadSeed == null || t.threadProbe == null) {
                t.threadSeed = null;
                t.threadProbe = null;
            }
            return t;
        }

        private void addSeeder(Class<?> owner, String name) throws Exception {
            Field f = field(owner.getName(), name);
            if (f != null) {
                seeders.add((AtomicLong) f.get(null));
            }
        }

        // reseed resets every target from the nonce mixed with /dev/urandom.
        String reseed(String nonceHex) throws Exception {
            byte[] nonce = hex(nonceHex);
            if (nonce.length < 16) {
                return "err bad_nonce";
            }
            byte[] fresh = urandom(32);
            for (int i = 0; i < fresh.length; i++) {
                fresh[i] ^= nonce[i % nonce.length];
            }
            SplitMix mix = new SplitMix(fresh);
            for (Random holder : holders) {
                holder.setSeed(mix.next());
            }
            for (AtomicLong seeder : seeders) {
                seeder.set(mix.next());
            }
            if (threadSeed != null) {
                for (Thread thread : Thread.getAllStackTraces().keySet()) {
                    // A probe of 0 marks a thread that never used
                    // ThreadLocalRandom; it seeds itself from the seeder.
                    if (threadProbe.getInt(thread) != 0) {
                        threadSeed.setLong(thread, mix.next());
                    }
                }
            }
            return "ok";
        }
    }

    private static Field field(String className, String name) throws Exception {
        Class<?> owner;
        try {
            owner = Class.forName(className, true, null);
        } catch (ClassNotFoundException missing) {
            return null;
        }
        Field f;
        try {
            f = owner.getDeclaredField(name);
        } catch (NoSuchFieldException missing) {
            return null;
        }
        f.setAccessible(true);
        return f;
    }

    // openJavaBase opens java.lang, java.util and java.util.concurrent to this
    // agent on Java 9+ (Instrumentation.redefineModule). Java 8 has no modules.
    private static void openJavaBase(Instrumentation inst) throws Exception {
        Method getModule;
        try {
            getModule = Class.class.getMethod("getModule");
        } catch (NoSuchMethodException java8) {
            return;
        }
        Object base = getModule.invoke(Object.class);
        Object self = getModule.invoke(Agent.class);
        Class<?> moduleClass = Class.forName("java.lang.Module");
        Map<String, Set<Object>> opens = new HashMap<String, Set<Object>>();
        for (String pkg : new String[] {"java.lang", "java.util", "java.util.concurrent"}) {
            opens.put(pkg, Collections.singleton(self));
        }
        Method redefine = Instrumentation.class.getMethod("redefineModule", moduleClass, Set.class, Map.class, Map.class, Set.class, Map.class);
        redefine.invoke(inst, base, new HashSet<Object>(), new HashMap<String, Set<Object>>(), opens, new HashSet<Class<?>>(), new HashMap<Class<?>, List<Class<?>>>());
    }

    private static byte[] urandom(int n) throws IOException {
        byte[] out = new byte[n];
        InputStream in = new FileInputStream("/dev/urandom");
        try {
            int off = 0;
            while (off < n) {
                int r = in.read(out, off, n - off);
                if (r < 0) {
                    throw new IOException("short read from /dev/urandom");
                }
                off += r;
            }
        } finally {
            in.close();
        }
        return out;
    }

    private static byte[] hex(String s) {
        if (s.length() % 2 != 0) {
            return new byte[0];
        }
        byte[] out = new byte[s.length() / 2];
        for (int i = 0; i < out.length; i++) {
            int hi = Character.digit(s.charAt(2 * i), 16);
            int lo = Character.digit(s.charAt(2 * i + 1), 16);
            if (hi < 0 || lo < 0) {
                return new byte[0];
            }
            out[i] = (byte) ((hi << 4) | lo);
        }
        return out;
    }

    // SplitMix derives independent 64-bit seeds from the fresh bytes.
    static final class SplitMix {
        private long state;

        SplitMix(byte[] seed) {
            for (int i = 0; i < seed.length; i++) {
                state = state * 31 + (seed[i] & 0xff);
                state ^= state >>> 29;
            }
            for (int i = 0; i + 8 <= seed.length; i += 8) {
                long word = 0;
                for (int j = 0; j < 8; j++) {
                    word = (word << 8) | (seed[i + j] & 0xff);
                }
                state ^= word;
                next();
            }
        }

        long next() {
            long z = (state += 0x9E3779B97F4A7C15L);
            z = (z ^ (z >>> 30)) * 0xBF58476D1CE4E5B9L;
            z = (z ^ (z >>> 27)) * 0x94D049BB133111EBL;
            return z ^ (z >>> 31);
        }
    }
}
