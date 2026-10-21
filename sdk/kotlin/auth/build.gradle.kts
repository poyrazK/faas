plugins {
    kotlin("jvm")
}

group = "dev.gregale"
version = "0.1.0"

kotlin {
    // Java 8 bytecode runs on Android 8+ (API 26, which has java.util.Base64)
    // and any JDK that runs Gradle.
    compilerOptions { jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_1_8) }
    explicitApi()
}

java {
    sourceCompatibility = JavaVersion.VERSION_1_8
    targetCompatibility = JavaVersion.VERSION_1_8
}

dependencies {
    api("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.11.0")
    api("org.jetbrains.kotlinx:kotlinx-serialization-json:1.11.0")
    testImplementation(kotlin("test"))
}

tasks.test {
    useJUnitPlatform()
    // The live test reads GREGALE_AUTH_LIVE_* from the environment.
    environment(System.getenv().filterKeys { it.startsWith("GREGALE_AUTH_LIVE_") })
}
