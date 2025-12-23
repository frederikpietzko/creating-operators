val koog_version: String by project
val kotlin_version: String by project
val logback_version: String by project

plugins {
    kotlin("jvm") version "2.3.0"
    id("io.ktor.plugin") version "3.3.2"
    id("org.jetbrains.kotlin.plugin.serialization") version "2.2.21"
    id("io.fabric8.java-generator") version "7.3.1"
}

group = "io.kops.webapp"
version = "0.0.1"

javaGen {
    source = file("src/main/resources/kubernetes/crds")
}

sourceSets {
    main {
        java.srcDir("build/generated/sources")
    }
}

application {
    mainClass = "io.ktor.server.netty.EngineMain"
}

dependencies {
    implementation("io.ktor:ktor-server-core")
    implementation("io.ktor:ktor-server-auto-head-response")
    implementation("io.ktor:ktor-server-call-logging")
    implementation("io.ktor:ktor-server-call-id")
    implementation("com.sksamuel.cohort:cohort-ktor:2.7.2")
    implementation("io.ktor:ktor-server-content-negotiation")
    implementation("io.ktor:ktor-serialization-kotlinx-json")
    implementation("ai.koog:koog-ktor:$koog_version")
    implementation("io.ktor:ktor-server-netty")
    implementation("ch.qos.logback:logback-classic:$logback_version")
    implementation("io.ktor:ktor-server-config-yaml")
    implementation("io.ktor:ktor-server-html-builder:3.3.2")
    implementation("io.fabric8:generator-annotations:7.3.1")
    implementation("org.jetbrains.kotlinx:kotlinx-html:0.12.0")
    implementation("io.ktor:ktor-server-htmx:3.3.2")
    implementation("io.ktor:ktor-htmx-html:3.3.2")
    implementation("io.fabric8:kubernetes-client:7.3.1")
    testImplementation("io.ktor:ktor-server-test-host")
    testImplementation("org.jetbrains.kotlin:kotlin-test-junit:$kotlin_version")
}
