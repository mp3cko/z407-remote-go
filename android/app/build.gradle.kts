plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// Version: -PversionName=1.2.3 (the release workflow passes the tag).
val appVersion = (findProperty("versionName") as String?)?.removePrefix("v") ?: "0.0.0-dev"
// 1.2.3 -> 10203; pre-release suffixes ("-rc.1") share the code of their release.
val appVersionCode = Regex("""^(\d+)\.(\d+)\.(\d+)""").find(appVersion)?.destructured
    ?.let { (major, minor, patch) -> major.toInt() * 10000 + minor.toInt() * 100 + patch.toInt() }
    ?.coerceAtLeast(1) ?: 1

// Release signing key, from the environment (GitHub Secrets in CI). Without
// it the release build is signed with the debug key so that it still installs.
val keystoreFile = System.getenv("Z407_KEYSTORE_FILE")?.takeIf { it.isNotBlank() && file(it).isFile }

android {
    namespace = "io.github.mp3cko.z407"
    compileSdk = 35
    buildToolsVersion = "35.0.0"

    defaultConfig {
        applicationId = "io.github.mp3cko.z407"
        minSdk = 26
        targetSdk = 35
        versionCode = appVersionCode
        versionName = appVersion
    }

    signingConfigs {
        if (keystoreFile != null) {
            create("release") {
                storeFile = file(keystoreFile)
                storePassword = System.getenv("Z407_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("Z407_KEY_ALIAS")
                keyPassword = System.getenv("Z407_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.findByName("release") ?: signingConfigs.getByName("debug")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }

    // The page shown by the app is the desktop app's page: one source of truth.
    sourceSets["main"].assets.srcDir(layout.buildDirectory.dir("generated/webui"))
}

// Copies the web UI from the Go sources into the app's assets.
val copyWebUi by tasks.registering(Copy::class) {
    from(rootProject.file("../internal/server/index.html"))
    from(rootProject.file("../internal/server/static")) {
        include("icon-192.png") // the page's touch icon; nothing else is used inside the app
    }
    // What the app serves to browsers on the network, next to the page.
    from(rootProject.file("../internal/server/static")) { into("static") }
    into(layout.buildDirectory.dir("generated/webui"))
}
tasks.named("preBuild") { dependsOn(copyWebUi) }

dependencies {
    implementation("androidx.webkit:webkit:1.12.1")
    testImplementation("junit:junit:4.13.2")
}
