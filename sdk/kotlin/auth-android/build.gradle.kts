// Android Gradle Plugin 9 compiles Kotlin itself.
plugins {
    id("com.android.library") version "9.4.1"
}

android {
    namespace = "dev.gregale.auth.android"
    compileSdk = 36
    defaultConfig { minSdk = 26 }
}

dependencies {
    api(project(":auth"))
}
