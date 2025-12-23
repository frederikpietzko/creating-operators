package io.kops.webapp

import ai.koog.ktor.Koog
import io.ktor.server.application.*

fun Application.configureFrameworks() {
    install(Koog) {}
}
