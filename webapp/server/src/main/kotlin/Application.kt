package io.kops.webapp

import io.fabric8.kubernetes.api.model.KubernetesResourceList
import io.kops.webapp.v1alpha1.WebappRepository
import io.ktor.server.application.*

fun main(args: Array<String>) {
    io.ktor.server.netty.EngineMain.main(args)
}

fun Application.module() {
    configureMonitoring()
    configureSerialization()
    configureKubernetes()
    configureFrameworks()
    configureRouting()
}
