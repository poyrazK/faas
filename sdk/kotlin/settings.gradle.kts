rootProject.name = "gregale-auth"

pluginManagement {
    repositories {
        gradlePluginPortal()
        google()
        mavenCentral()
    }
}

dependencyResolutionManagement {
    repositories {
        google()
        mavenCentral()
    }
}

include("auth")
// The Android module needs the Android SDK (ANDROID_HOME or local.properties).
if (System.getenv("ANDROID_HOME") != null || file("local.properties").exists()) include("auth-android")
