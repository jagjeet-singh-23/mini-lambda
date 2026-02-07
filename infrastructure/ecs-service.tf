# ECS Task Definition
resource "aws_ecs_task_definition" "app" {
  family                   = var.app_name
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = var.environment == "production" ? "1024" : "512"
  memory                   = var.environment == "production" ? "2048" : "1024"
  
  execution_role_arn = aws_iam_role.ecs_task_execution.arn
  task_role_arn      = aws_iam_role.ecs_task.arn

  container_definitions = jsonencode([{
    name      = var.app_name
    image     = "${var.ecr_repository}:${var.image_tag}"
    essential = true

    portMappings = [{
      containerPort = 8080
      protocol      = "tcp"
    }]

    environment = [
      {
        name  = "POSTGRES_HOST"
        value = split(":", aws_db_instance.postgres.endpoint)[0]
      },
      {
        name  = "POSTGRES_PORT"
        value = "5432"
      },
      {
        name  = "POSTGRES_DB"
        value = aws_db_instance.postgres.db_name
      },
      {
        name  = "POSTGRES_USER"
        value = aws_db_instance.postgres.username
      },
      {
        name  = "RABBITMQ_HOST"
        value = aws_mq_broker.rabbitmq.instances[0].endpoints[0]
      },
      {
        name  = "S3_BUCKET"
        value = aws_s3_bucket.lambda_functions.id
      },
      {
        name  = "S3_REGION"
        value = var.aws_region
      },
      {
        name  = "ENVIRONMENT"
        value = var.environment
      }
    ]

    secrets = [
      {
        name      = "POSTGRES_PASSWORD"
        valueFrom = aws_secretsmanager_secret.db_password.arn
      },
      {
        name      = "RABBITMQ_PASSWORD"
        valueFrom = aws_secretsmanager_secret.mq_password.arn
      }
    ]

    logConfiguration = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = aws_cloudwatch_log_group.app.name
        "awslogs-region"        = var.aws_region
        "awslogs-stream-prefix" = "ecs"
      }
    }

    healthCheck = {
      command     = ["CMD-SHELL", "curl -f http://localhost:8080/health || exit 1"]
      interval    = 30
      timeout     = 5
      retries     = 3
      startPeriod = 60
    }
  }])

  tags = {
    Name = "${var.app_name}-task"
  }
}

# Secrets Manager for Passwords
resource "aws_secretsmanager_secret" "db_password" {
  name                    = "${var.app_name}-db-password-${random_id.secret_suffix.hex}"
  description             = "RDS PostgreSQL password"
  recovery_window_in_days = var.environment == "production" ? 30 : 0

  tags = {
    Name = "${var.app_name}-db-password"
  }
}

resource "aws_secretsmanager_secret_version" "db_password" {
  secret_id     = aws_secretsmanager_secret.db_password.id
  secret_string = random_password.db_password.result
}

resource "aws_secretsmanager_secret" "mq_password" {
  name                    = "${var.app_name}-mq-password-${random_id.secret_suffix.hex}"
  description             = "Amazon MQ password"
  recovery_window_in_days = var.environment == "production" ? 30 : 0

  tags = {
    Name = "${var.app_name}-mq-password"
  }
}

resource "aws_secretsmanager_secret_version" "mq_password" {
  secret_id     = aws_secretsmanager_secret.mq_password.id
  secret_string = random_password.mq_password.result
}

resource "random_id" "secret_suffix" {
  byte_length = 4
}

# ECS Service
resource "aws_ecs_service" "app" {
  name            = "${var.app_name}-service"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.app.arn
  desired_count   = var.environment == "production" ? 3 : 1
  launch_type     = "FARGATE"

  platform_version = "LATEST"

  network_configuration {
    subnets          = module.vpc.private_subnets
    security_groups  = [aws_security_group.app.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.app.arn
    container_name   = var.app_name
    container_port   = 8080
  }

  deployment_maximum_percent         = 200
  deployment_minimum_healthy_percent = 100

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  enable_execute_command = true

  tags = {
    Name = "${var.app_name}-service"
  }

  depends_on = [
    aws_lb_listener.http,
    aws_iam_role_policy.ecs_task_s3
  ]
}

# Auto Scaling
resource "aws_appautoscaling_target" "ecs" {
  count              = var.environment == "production" ? 1 : 0
  max_capacity       = 10
  min_capacity       = 3
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.app.name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

resource "aws_appautoscaling_policy" "ecs_cpu" {
  count              = var.environment == "production" ? 1 : 0
  name               = "${var.app_name}-cpu-autoscaling"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.ecs[0].resource_id
  scalable_dimension = aws_appautoscaling_target.ecs[0].scalable_dimension
  service_namespace  = aws_appautoscaling_target.ecs[0].service_namespace

  target_tracking_scaling_policy_configuration {
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageCPUUtilization"
    }
    target_value       = 70.0
    scale_in_cooldown  = 300
    scale_out_cooldown = 60
  }
}

resource "aws_appautoscaling_policy" "ecs_memory" {
  count              = var.environment == "production" ? 1 : 0
  name               = "${var.app_name}-memory-autoscaling"
  policy_type        = "TargetTrackingScaling"
  resource_id        = aws_appautoscaling_target.ecs[0].resource_id
  scalable_dimension = aws_appautoscaling_target.ecs[0].scalable_dimension
  service_namespace  = aws_appautoscaling_target.ecs[0].service_namespace

  target_tracking_scaling_policy_configuration {
    predefined_metric_specification {
      predefined_metric_type = "ECSServiceAverageMemoryUtilization"
    }
    target_value       = 80.0
    scale_in_cooldown  = 300
    scale_out_cooldown = 60
  }
}

# Additional variables needed
variable "ecr_repository" {
  description = "ECR repository URL"
  type        = string
  default     = "ghcr.io/your-username/mini-lambda"
}

variable "image_tag" {
  description = "Docker image tag"
  type        = string
  default     = "latest"
}

# Outputs for ECS
output "ecs_service_name" {
  value = aws_ecs_service.app.name
}

output "ecs_task_definition_arn" {
  value = aws_ecs_task_definition.app.arn
}